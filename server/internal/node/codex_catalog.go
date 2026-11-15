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
// URL is passed only where the node found a catalog there just now.
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

// codexCatalogTTL: a probe's answer, a catalog or none, is kept this long per gateway, key and Codex version.
const codexCatalogTTL = 10 * time.Minute

// codexCatalogMost is the most of an answer the probe reads; a larger one is not taken for a catalog.
const codexCatalogMost = 32 << 20

var codexCatalogCache = struct {
	sync.Mutex
	at     map[string]time.Time
	url    map[string]string
	flight singleflight.Group
}{at: map[string]time.Time{}, url: map[string]string{}}

// codexCatalogURL is the model catalog URL for l's codex (no client_version: Codex adds its own), or "" when l is no
// Codex provider with a gateway and a key, this computer's codex is too old to read one, or its gateway serves none.
func (n *Node) codexCatalogURL(ctx context.Context, l proto.ManagedLauncher) string {
	if l.AgentType() != "codex" || codexChoosesProvider(l.Args) {
		return ""
	}
	env := managedEnv(l)
	base, key := strings.TrimRight(envValue(env, "OPENAI_BASE_URL"), "/"), envValue(env, "OPENAI_API_KEY")
	if u, err := url.Parse(base); base == "" || key == "" || err != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	version := codexVersion(ctx)
	if !codexReadsCatalog(version) {
		return ""
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
	codexCatalogCache.Lock()
	at, ok := codexCatalogCache.at[cacheKey]
	found := codexCatalogCache.url[cacheKey]
	codexCatalogCache.Unlock()
	if ok && time.Since(at) < codexCatalogTTL {
		return found
	}
	v, _, _ := codexCatalogCache.flight.Do(cacheKey, func() (any, error) {
		found := probeCodexCatalog(context.WithoutCancel(ctx), base, key, version, gw)
		if found != "" {
			n.logf("launcher %s: its gateway serves Codex's model catalog at %s", l.Name, found)
		}
		codexCatalogCache.Lock()
		if len(codexCatalogCache.at) > 64 {
			codexCatalogCache.at, codexCatalogCache.url = map[string]time.Time{}, map[string]string{}
		}
		codexCatalogCache.at[cacheKey], codexCatalogCache.url[cacheKey] = time.Now(), found
		codexCatalogCache.Unlock()
		return found, nil
	})
	return v.(string)
}

// probeCodexCatalog asks each of codexCatalogPaths below base at once, as Codex would ask its model_catalog_url, and
// is the first, in that order, that answers with a catalog; "" when none does in time.
func probeCodexCatalog(ctx context.Context, base, key, version string, env map[string]string) string {
	ctx, cancel := context.WithTimeout(ctx, codexCatalogTimeout)
	defer cancel()
	client := gatewayClient(env) // no redirect: Codex follows none for a catalog either
	ok := make([]chan bool, len(codexCatalogPaths))
	for i, path := range codexCatalogPaths {
		ok[i] = make(chan bool, 1)
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+url.Values{"client_version": {codexCatalogClientVersion(version)}}.Encode(), nil)
			if err != nil {
				ok[i] <- false
				return
			}
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Accept", "application/json")
			modelhttp.SetDefaultUserAgent(req.Header) // Persona's own request, as its other gateway requests (#8775)
			resp, err := client.Do(req)
			if err != nil {
				ok[i] <- false
				return
			}
			defer resp.Body.Close()
			ok[i] <- resp.StatusCode == http.StatusOK && isCodexCatalog(resp.Body)
		}()
	}
	for i, path := range codexCatalogPaths {
		if <-ok[i] {
			return base + path
		}
	}
	return ""
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
}{}

// codexVersion is this computer's codex's version as Codex sends it with a catalog request (major.minor.patch, from
// `codex --version`), with a "-" suffix for a prerelease; "" when there is no codex or it says no version. Kept while
// the executable is unchanged.
func codexVersion(ctx context.Context) string {
	exe, err := agentExecutable("codex")
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	stamp := exe + "\x00" + st.ModTime().String() + "\x00" + strconv.FormatInt(st.Size(), 10)
	codexVersionCache.Lock()
	kept, version := codexVersionCache.stamp == stamp, codexVersionCache.version
	codexVersionCache.Unlock()
	if kept {
		return version
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	if err != nil { // asked again next time: one slow or failed run says nothing of the version
		return ""
	}
	version = parseCodexVersion(string(out))
	codexVersionCache.Lock()
	codexVersionCache.stamp, codexVersionCache.version = stamp, version
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
