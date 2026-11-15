package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/nwverse/persona/server/internal/modelhttp"
	"github.com/nwverse/persona/server/internal/proto"
)

// A managed Codex provider's gateway may serve Codex's own model catalog: each model's context window, compaction
// limit, thinking levels and instructions, in the shape Codex reads from its backend (codex-rs ModelInfo). Without it
// Codex treats the gateway as OpenAI: its picker lists the bundled OpenAI models and a model only the gateway has runs
// on fallback metadata. Codex reads a catalog from a provider's model_catalog_url (0.156 and later; by default since
// 0.161, when api_key_model_discovery became stable), asking `<url>?client_version=<its version>` with the provider's
// key. Once a provider has one, it is the only list Codex uses: one that fails leaves Codex with no list, so the
// URL is passed only where the node found a catalog there, and kept through a probe that could not tell.
//
// The node looks for it by the answer's shape, never the gateway's name (docs/integrations/codex.md, "Gateway model
// catalog"): the probe asks what Codex would, and a catalog is an object whose `models` are Codex model entries.

// codexCatalogPaths are where, below the provider's base URL (the one ending in /v1), a gateway may serve the catalog,
// most likely first: Codex's own path for its backend's catalog (some gateways answer it in Codex's shape when the
// request carries client_version, and with OpenAI's list otherwise), then /codex/models (a gateway that keeps /models
// for OpenAI's list).
var codexCatalogPaths = []string{"/models", "/codex/models"}

// codexCatalogMinVersion is the first Codex that reads a provider's catalog without a feature switch.
var codexCatalogMinVersion = [3]int{0, 161, 0}

// codexCatalogTimeout bounds the whole probe (both paths at once), as Codex bounds its own catalog request.
var codexCatalogTimeout = 5 * time.Second

// codexCatalogTTL: an answer, a catalog or none, is fresh this long per gateway, key, proxy and Codex version.
const codexCatalogTTL = 10 * time.Minute

// codexCatalogStale: an answer older than codexCatalogTTL but younger than this is still given at once, and the gateway
// is asked again in the background for the next start: once a gateway has answered, a start never waits on the probe.
const codexCatalogStale = 24 * time.Hour

// codexCatalogRetry: after a probe that could not tell (no answer in time, a 5xx, 408 or 429), or a codex --version
// that failed, it is tried again this soon; a catalog found before is kept meanwhile.
const codexCatalogRetry = time.Minute

// codexCatalogMost is the most of an answer the probe reads; a larger one is not taken for a catalog.
const codexCatalogMost = 32 << 20

// codexCatalogAnswer is what the probe last learned of a gateway.
type codexCatalogAnswer struct {
	url   string    // the catalog, "" for none
	at    time.Time // when the gateway said so
	fresh time.Time // until when it is not asked again
}

var codexCatalogCache = struct {
	sync.Mutex
	got    map[string]codexCatalogAnswer
	flight singleflight.Group
}{got: map[string]codexCatalogAnswer{}}

// codexCatalog is the model catalog URL for l's codex (no client_version: Codex adds its own) and the codex executable
// whose version allowed it; "" when l is no Codex provider with a gateway and a key, this computer's codex is too old
// to read one, or its gateway serves none.
func (n *Node) codexCatalog(ctx context.Context, l proto.ManagedLauncher) (catalog, codex string) {
	if l.AgentType() != "codex" || codexChoosesProvider(l.Args) {
		return "", ""
	}
	env := managedEnv(l)
	base, key := strings.TrimRight(envValue(env, "OPENAI_BASE_URL"), "/"), envValue(env, "OPENAI_API_KEY")
	if u, err := url.Parse(base); base == "" || key == "" || err != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ""
	}
	exe, version := codexVersion(ctx)
	if !codexReadsCatalog(version) {
		return "", ""
	}
	gw := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			gw[k] = v
		}
	}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			gw[k] = v
		}
	}
	sum := sha256.Sum256([]byte(key))
	cacheKey := strings.Join([]string{version, base, hex.EncodeToString(sum[:]), gw["http_proxy"], gw["HTTP_PROXY"],
		gw["https_proxy"], gw["HTTPS_PROXY"], gw["no_proxy"], gw["NO_PROXY"]}, "\x00")
	ask := func() (any, error) {
		found, sure := probeCodexCatalog(context.WithoutCancel(ctx), base, key, version, gw)
		now := time.Now()
		codexCatalogCache.Lock()
		defer codexCatalogCache.Unlock()
		prev, had := codexCatalogCache.got[cacheKey]
		a := codexCatalogAnswer{url: found, at: now, fresh: now.Add(codexCatalogTTL)}
		if found == "" && !sure {
			a.fresh = now.Add(codexCatalogRetry)
			if had && prev.url != "" { // a gateway that did not answer this time still has the catalog it had
				a.url, a.at = prev.url, prev.at
			}
		}
		if found != "" && prev.url != found {
			n.logf("launcher %s: its gateway serves Codex's model catalog at %s", l.Name, found)
		}
		if !had && len(codexCatalogCache.got) >= 64 {
			codexCatalogCache.got = map[string]codexCatalogAnswer{}
		}
		codexCatalogCache.got[cacheKey] = a
		return a.url, nil
	}
	codexCatalogCache.Lock()
	a, ok := codexCatalogCache.got[cacheKey]
	codexCatalogCache.Unlock()
	switch now := time.Now(); {
	case ok && now.Before(a.fresh):
	case ok && now.Sub(a.at) < codexCatalogStale:
		codexCatalogCache.flight.DoChan(cacheKey, ask) // for the next start; one already asking is joined
	default:
		v, _, _ := codexCatalogCache.flight.Do(cacheKey, ask)
		a.url = v.(string)
	}
	if a.url == "" {
		return "", ""
	}
	return a.url, exe
}

// probeCodexCatalog asks each of codexCatalogPaths below base at once, as Codex would ask its model_catalog_url, and
// is the first, in that order, that answers with a catalog. With none, sure says whether every path answered (an
// answer that is no catalog), not an error, no answer in time or a status that may pass (5xx, 408, 429).
func probeCodexCatalog(ctx context.Context, base, key, version string, env map[string]string) (catalog string, sure bool) {
	const (
		none = iota
		found
		unsure
	)
	ctx, cancel := context.WithTimeout(ctx, codexCatalogTimeout)
	defer cancel()
	client := gatewayClient(env) // no redirect: Codex follows none for a catalog either
	got := make([]chan int, len(codexCatalogPaths))
	for i, path := range codexCatalogPaths {
		got[i] = make(chan int, 1)
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+url.Values{"client_version": {codexCatalogClientVersion(version)}}.Encode(), nil)
			if err != nil {
				got[i] <- none
				return
			}
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Accept", "application/json")
			modelhttp.SetDefaultUserAgent(req.Header) // Persona's own request, as its other gateway requests (#8775)
			resp, err := client.Do(req)
			switch {
			case err != nil:
				got[i] <- unsure
				return
			case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooManyRequests:
				resp.Body.Close()
				got[i] <- unsure
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK && isCodexCatalog(resp.Body) {
				got[i] <- found
				return
			}
			got[i] <- none
		}()
	}
	sure = true
	for i, path := range codexCatalogPaths {
		switch <-got[i] {
		case found:
			return base + path, true
		case unsure:
			sure = false
		}
	}
	return "", sure
}

// isCodexCatalog: r is a catalog Codex can use, `{"models":[…]}` with at least one entry, each with the fields Codex
// cannot do without in every version that reads one (a unique, non-empty slug, a display name, its thinking levels and
// its visibility), and one at least that its picker lists. OpenAI's model list ({"data":[…]}) is none.
func isCodexCatalog(r io.Reader) bool {
	var doc struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(r, codexCatalogMost)).Decode(&doc) != nil || len(doc.Models) == 0 {
		return false
	}
	text := func(raw json.RawMessage) (string, bool) {
		var s string
		return s, len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil
	}
	seen, listed := map[string]bool{}, false
	for _, m := range doc.Models {
		slug, ok := text(m["slug"])
		if !ok || strings.TrimSpace(slug) == "" || seen[slug] {
			return false
		}
		seen[slug] = true
		levels := m["supported_reasoning_levels"]
		var list []json.RawMessage
		if _, ok := text(m["display_name"]); !ok || len(levels) == 0 || levels[0] != '[' || json.Unmarshal(levels, &list) != nil {
			return false
		}
		visibility, ok := text(m["visibility"])
		if !ok {
			return false
		}
		listed = listed || visibility == "list"
	}
	return listed
}

var codexVersionCache = struct {
	sync.Mutex
	stamp, version string
	until          time.Time // a failed run's: asked again after it; zero for an answer, kept while the file is
}{}

// codexVersion is this computer's codex (agentExecutable's) and its version (codexVersionAt).
func codexVersion(ctx context.Context) (exe, version string) {
	exe, err := agentExecutable("codex")
	if err != nil {
		return "", ""
	}
	return exe, codexVersionAt(ctx, exe)
}

// codexVersionAt is the version of the codex at exe as Codex sends it with a catalog request (major.minor.patch, from
// `codex --version`), with a "-" suffix for a prerelease; "" when it says none or cannot be run. Kept while the
// executable is unchanged; a run that failed or hung is tried again after codexCatalogRetry, not on every start.
func codexVersionAt(ctx context.Context, exe string) string {
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	stamp := exe + "\x00" + st.ModTime().String() + "\x00" + strconv.FormatInt(st.Size(), 10)
	codexVersionCache.Lock()
	kept := codexVersionCache.stamp == stamp && (codexVersionCache.until.IsZero() || time.Now().Before(codexVersionCache.until))
	version, until := codexVersionCache.version, time.Time{}
	codexVersionCache.Unlock()
	if kept {
		return version
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	version, until = parseCodexVersion(string(out)), time.Time{}
	if err != nil {
		version, until = "", time.Now().Add(codexCatalogRetry)
	}
	codexVersionCache.Lock()
	codexVersionCache.stamp, codexVersionCache.version, codexVersionCache.until = stamp, version, until
	codexVersionCache.Unlock()
	return version
}

// parseCodexVersion reads `codex --version`'s "codex-cli 0.167.0": its first word that is a version, "0.167.0", or
// "0.168.0-" for a prerelease (0.168.0-alpha.2); "" when there is none.
func parseCodexVersion(out string) string {
	for _, word := range strings.Fields(out) {
		v, _, pre := strings.Cut(strings.SplitN(word, "+", 2)[0], "-")
		parts := strings.Split(v, ".")
		if len(parts) != 3 || slices.ContainsFunc(parts, func(p string) bool { _, err := strconv.Atoi(p); return err != nil }) {
			continue
		}
		if pre {
			return v + "-"
		}
		return v
	}
	return ""
}

// codexReadsCatalog: a codex of version (parseCodexVersion's) reads a provider's catalog by default; a prerelease of
// the first one that does is taken for one that does not yet.
func codexReadsCatalog(version string) bool {
	if version == "" {
		return false
	}
	v, pre := strings.CutSuffix(version, "-")
	var got [3]int
	for i, p := range strings.Split(v, ".") {
		got[i], _ = strconv.Atoi(p)
	}
	for i := range got {
		if got[i] != codexCatalogMinVersion[i] {
			return got[i] > codexCatalogMinVersion[i]
		}
	}
	return !pre
}

// codexCatalogClientVersion is the client_version Codex sends for version: major.minor.patch, no prerelease.
func codexCatalogClientVersion(version string) string { return strings.TrimSuffix(version, "-") }

// sameFile: paths a and b name one file (a link and its target too); false when either is empty or missing.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && os.SameFile(sa, sb)
}
