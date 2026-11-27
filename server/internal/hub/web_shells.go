package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

const (
	// webShellCookie keeps this browser's chosen Web Shell ("<extension>/<shell-id>"); absent or "builtin" uses the built-in Web UI.
	// Custom shells are only activated from the Hub origin (Settings / same-origin cookie), never via GET query parameters.
	webShellCookie = "persona_web_shell"
	// webShellQuery is the guaranteed server-side escape hatch: "?shell=builtin" clears webShellCookie and HTTP/SW caches.
	webShellQuery = "shell"
	// webShellHeader marks every response served from a custom Web Shell extension so the built-in Service Worker never caches it.
	webShellHeader = "X-Persona-Web-Shell"
	// webShellFetchTimeout bounds one on-demand HubWebShellBundle RPC fetch from the owning manager.
	webShellFetchTimeout = 15 * time.Second
)

type cachedWebShell struct {
	key      string
	selector string // "<extension>/<shell-id>"
	fs       fstest.MapFS
	tags     *rootTags
}

var extWebShells = struct {
	mu    sync.Mutex
	items map[string]*cachedWebShell
}{items: map[string]*cachedWebShell{}}

// webHandler wraps the built-in webApp with opt-in Web Shell extension serving and a guaranteed "?shell=builtin" escape hatch (#15396).
func (h *Hub) webHandler(dir string) http.Handler {
	return webWithShells(webApp(dir), h.findWebShell)
}

func webWithShells(builtin http.Handler, lookup func(ctx context.Context, ext, id string) (*cachedWebShell, bool)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			builtin.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Cookie")

		isSW := strings.EqualFold(r.Header.Get("Service-Worker"), "script")
		q := r.URL.Query()
		hasShellQuery := q.Has(webShellQuery)

		// A custom Web Shell may never register a Hub-origin Service Worker (which could intercept future navigations and
		// break the ?shell=builtin escape hatch). Returning 404 to Service-Worker: script fetches also instructs browsers
		// to unregister any previously installed built-in SW on update checks while a custom shell is active.
		if isSW && (hasShellQuery || hasCustomWebShellCookie(r)) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}

		// Handle "?shell=..." strictly as an escape hatch to return to the built-in Web UI.
		// Non-builtin values (including empty "?shell=") are stripped without modifying cookies, preventing cross-site activation.
		if hasShellQuery {
			choice := strings.TrimSpace(q.Get(webShellQuery))
			if slices.Contains(extension.BuiltinWebShellIDs, choice) {
				cPath := webShellCookiePath(r)
				secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
				http.SetCookie(w, &http.Cookie{
					Name:     webShellCookie,
					Value:    "",
					Path:     cPath,
					MaxAge:   -1,
					Secure:   secure,
					SameSite: http.SameSiteLaxMode,
				})
				// Clear HTTP & CacheStorage caches without touching localStorage credentials.
				w.Header().Set("Clear-Site-Data", `"cache"`)
			}
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, cleanShellRedirect(r), http.StatusFound)
			return
		}

		// No active cookie or unknown/disabled shell falls back seamlessly to the built-in Web UI.
		var chosen *cachedWebShell
		if lookup != nil {
			if ext, id, ok := readWebShellCookie(r); ok {
				if shell, exists := lookup(r.Context(), ext, id); exists {
					chosen = shell
				}
			}
		}
		if chosen == nil {
			builtin.ServeHTTP(w, r)
			return
		}
		serveWebShell(w, r, chosen, builtin)
	})
}

// readWebShellCookie reads persona_web_shell, accepting both raw ("ext/id") and percent-encoded ("ext%2Fid") values.
func readWebShellCookie(r *http.Request) (ext, id string, ok bool) {
	c, err := r.Cookie(webShellCookie)
	if err != nil {
		return "", "", false
	}
	raw := c.Value
	if unescaped, err := url.PathUnescape(raw); err == nil {
		raw = unescaped
	}
	return extension.ParseWebShellKey(raw)
}

func hasCustomWebShellCookie(r *http.Request) bool {
	_, _, ok := readWebShellCookie(r)
	return ok
}

func webShellCookiePath(r *http.Request) string {
	if p := forwardedPrefix(r); p != "" {
		return p + "/"
	}
	return "/"
}

// cleanShellRedirect removes the "shell" query param and produces a strictly same-origin relative URI while
// preserving valid percent-encoded path segments such as deep-link IDs containing "%2F".
func cleanShellRedirect(r *http.Request) string {
	ep := r.URL.EscapedPath()
	lower := strings.ToLower(ep)
	if ep == "" || !strings.HasPrefix(ep, "/") || strings.Contains(ep, `\`) || strings.Contains(lower, "%5c") || hasUnsafePathSegment(ep) {
		ep = "/"
	}
	ep = "/" + strings.TrimLeft(ep, "/")
	if prefix := forwardedPrefix(r); prefix != "" {
		ep = prefix + ep
	}
	q := r.URL.Query()
	q.Del(webShellQuery)
	if enc := q.Encode(); enc != "" {
		return ep + "?" + enc
	}
	return ep
}

func hasUnsafePathSegment(escapedPath string) bool {
	for i, seg := range strings.Split(escapedPath[1:], "/") {
		unescaped, err := url.PathUnescape(seg)
		if err != nil {
			return true
		}
		if unescaped == "." || unescaped == ".." {
			return true
		}
		// Reject a first segment that unescapes to a leading slash (e.g. "/%2f/evil.com") or any backslash/control char.
		if i == 0 && strings.HasPrefix(unescaped, "/") {
			return true
		}
		for _, ch := range unescaped {
			if ch < 0x20 || ch == 0x7f || ch == '\\' {
				return true
			}
		}
	}
	return false
}

func serveWebShell(w http.ResponseWriter, r *http.Request, shell *cachedWebShell, builtin http.Handler) {
	w.Header().Set(webShellHeader, shell.selector)

	site := shell.fs
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

	// Allow custom Web Shells to share the built-in OPAQUE WASM auth assets under /pake/ without bundling Go WASM.
	if (name == "pake" || strings.HasPrefix(name, "pake/")) && !isFile(site, name) {
		builtin.ServeHTTP(w, r)
		return
	}

	file := name
	asset := strings.HasPrefix(name, "assets/")
	hidden := strings.HasPrefix(path.Base(name), ".")
	if name == "" || hidden || !isFile(site, name) {
		if asset {
			http.NotFound(w, r)
			return
		}
		file = "index.html"
	}

	// Custom Web Shell bundles are not required to content-hash asset filenames, so revalidate every file via ETag
	// rather than caching /assets/* as immutable across extension upgrades.
	w.Header().Set("Cache-Control", "private, no-cache")
	if path.Ext(file) == ".webmanifest" {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")

	if file == "index.html" {
		serveIndex(w, r, site, true, shell.tags)
		return
	}

	f, err := site.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	rs, ok := f.(io.ReadSeeker)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	if tag, ok := shell.tags.of(site, file); ok {
		setETag(w, r, tag, st.Size(), file)
	}
	if ct := mime.TypeByExtension(path.Ext(file)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeContent(w, r, path.Base(file), st.ModTime(), rs)
}

// webShellManagerOrder returns reporting managers in the exact same precedence as retainedListManagers
// (default manager first, then remaining managers sorted by ID) so discovery and serving always agree.
func (h *Hub) webShellManagerOrder(ctx context.Context) []string {
	def := h.defaultOwner(ctx)
	seen := map[string]bool{}
	var rest []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if id != def {
			rest = append(rest, id)
		}
	}
	add(assistantwire.AssistantID)
	h.ext.reportsMu.Lock()
	for id := range h.ext.reports {
		add(id)
	}
	h.ext.reportsMu.Unlock()
	sort.Strings(rest)
	if def != "" {
		return append([]string{def}, rest...)
	}
	return rest
}

type webShellCandidate struct {
	managerID string
	ref       extwire.WebShellRef
	item      extension.WebShellItem
}

// resolveWebShellCandidates returns every manager reporting (ext, id) in default-first precedence order.
func (h *Hub) resolveWebShellCandidates(ctx context.Context, ext, id string) []webShellCandidate {
	var out []webShellCandidate
	for _, mgrID := range h.webShellManagerOrder(ctx) {
		r := h.extReportOf(mgrID)
		if r == nil {
			continue
		}
		for _, ws := range r.WebShells {
			if ws.Extension != ext {
				continue
			}
			var decoded extension.WebShellItem
			if err := json.Unmarshal(ws.Item, &decoded); err != nil || decoded.ID != id {
				continue
			}
			out = append(out, webShellCandidate{managerID: mgrID, ref: ws, item: decoded})
			break
		}
	}
	return out
}

// resolveWebShellRef locates the winning manager and descriptor for (ext, id) using default-first manager precedence.
func (h *Hub) resolveWebShellRef(ctx context.Context, ext, id string) (managerID string, ref extwire.WebShellRef, item extension.WebShellItem, ok bool) {
	cands := h.resolveWebShellCandidates(ctx, ext, id)
	if len(cands) == 0 {
		return "", extwire.WebShellRef{}, extension.WebShellItem{}, false
	}
	return cands[0].managerID, cands[0].ref, cands[0].item, true
}

func webShellCacheKey(managerID string, ref extwire.WebShellRef, shellID string) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", managerID, ref.Extension, ref.Version, shellID, ref.Digest)
}

func (h *Hub) findWebShell(ctx context.Context, ext, id string) (*cachedWebShell, bool) {
	return h.findWebShellWithFetch(ctx, ext, id, func(callCtx context.Context, mgrID string) (extwire.WebShellBundle, error) {
		var bundle extwire.WebShellBundle
		err := h.extCallTo(callCtx, mgrID, extwire.HubWebShellBundle, map[string]string{"extension": ext, "id": id}, &bundle)
		return bundle, err
	})
}

func (h *Hub) findWebShellWithFetch(
	ctx context.Context,
	ext, id string,
	fetch func(ctx context.Context, mgrID string) (extwire.WebShellBundle, error),
) (*cachedWebShell, bool) {
	for _, cand := range h.resolveWebShellCandidates(ctx, ext, id) {
		cacheKey := webShellCacheKey(cand.managerID, cand.ref, cand.item.ID)
		extWebShells.mu.Lock()
		if hit := extWebShells.items[cacheKey]; hit != nil {
			extWebShells.mu.Unlock()
			return hit, true
		}
		extWebShells.mu.Unlock()

		callCtx, cancel := context.WithTimeout(ctx, webShellFetchTimeout)
		bundle, err := fetch(callCtx, cand.managerID)
		cancel()
		if err != nil {
			// If the higher-precedence manager is offline and uncached, continue to the next manager reporting this key,
			// matching mergeList's fallback to online managers when an offline manager answers 503.
			if h.logger != nil {
				h.logf("web_shell %s/%s from %s fetch failed: %v", ext, id, cand.managerID, err)
			}
			continue
		}
		shell, err := buildCachedWebShell(cacheKey, cand.managerID, bundle, cand.item)
		if err != nil {
			if h.logger != nil {
				h.logf("web_shell %s/%s from %s left out: %v", ext, id, cand.managerID, err)
			}
			continue
		}
		return shell, true
	}
	return nil, false
}

func buildCachedWebShell(cacheKey, managerID string, bundle extwire.WebShellBundle, item extension.WebShellItem) (*cachedWebShell, error) {
	if _, ok := bundle.Files["index.html"]; !ok {
		return nil, fmt.Errorf("index.html missing")
	}
	var names []string
	for name := range bundle.Files {
		names = append(names, name)
	}
	slices.Sort(names)

	if cacheKey == "" {
		digest := bundle.Digest
		if digest == "" {
			digest = extension.WebShellDigest(bundle.Files)
		}
		cacheKey = fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", managerID, bundle.Extension, bundle.Version, item.ID, digest)
	}

	extWebShells.mu.Lock()
	if hit := extWebShells.items[cacheKey]; hit != nil {
		extWebShells.mu.Unlock()
		return hit, nil
	}
	extWebShells.mu.Unlock()

	mfs := fstest.MapFS{}
	for _, name := range names {
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") {
			continue
		}
		data := bundle.Files[name]
		mfs[clean] = &fstest.MapFile{Data: data, Mode: fs.FileMode(0o644)}
	}
	shell := &cachedWebShell{
		key:      cacheKey,
		selector: extension.WebShellKey(bundle.Extension, item.ID),
		fs:       mfs,
		tags:     &rootTags{},
	}

	extWebShells.mu.Lock()
	if len(extWebShells.items) > 32 {
		extWebShells.items = map[string]*cachedWebShell{}
	}
	extWebShells.items[cacheKey] = shell
	extWebShells.mu.Unlock()
	return shell, nil
}

// writeMergedWebShells merges GET /api/v1/extensions/-/web-shells across managers in default-first order
// and deduplicates by canonical shell "key" so every listed entry matches the exact manager that findWebShell serves.
func writeMergedWebShells(w http.ResponseWriter, answers []listAnswer) {
	seen := map[string]bool{}
	merged := []map[string]any{}
	anyOK := false
	for _, a := range answers {
		if !a.ok() {
			continue
		}
		var doc struct {
			WebShells []map[string]any `json:"web_shells"`
		}
		if err := json.Unmarshal(a.body, &doc); err != nil {
			continue
		}
		anyOK = true
		for _, item := range doc.WebShells {
			key, _ := item["key"].(string)
			if key == "" {
				ext, _ := item["extension"].(string)
				id, _ := item["id"].(string)
				key = extension.WebShellKey(ext, id)
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			item["manager"] = a.id
			merged = append(merged, item)
		}
	}
	if !anyOK {
		writeMerged(w, "web_shells", answers)
		return
	}
	writeJSON(w, map[string]any{"web_shells": merged})
}
