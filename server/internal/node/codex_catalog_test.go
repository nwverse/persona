package node

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// codexCatalogBody is a Codex model catalog as a gateway serves it: a listed model with levels, one that takes none,
// and a hidden one (the fields Codex needs beyond these are left out: the probe does not read them).
const codexCatalogBody = `{"models":[` +
	`{"slug":"example-big","display_name":"Example Big","context_window":1000000,"visibility":"list",` +
	`"supported_reasoning_levels":[{"effort":"low","description":"low"},{"effort":"high","description":"high"}],"shell_type":"shell_command"},` +
	`{"slug":"example-plain","display_name":"Example Plain","context_window":64000,"visibility":"list","supported_reasoning_levels":[]},` +
	`{"slug":"example-hidden","display_name":"Example Hidden","visibility":"hide","supported_reasoning_levels":[]}]}`

// openAIModelsBody is OpenAI's model list, which Codex cannot read as a catalog.
const openAIModelsBody = `{"object":"list","data":[{"id":"example-big","object":"model"}]}`

// fakeCodex puts a codex on PATH that says it is version (codex --version), and forgets what earlier tests probed.
func fakeCodex(t *testing.T, version string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script as codex")
	}
	bin := t.TempDir()
	if version != "" {
		writeFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\necho 'codex-cli "+version+"'\n")
		if err := os.Chmod(filepath.Join(bin, "codex"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir()) // agentExecutable's fallbacks look under the home and in the system's places
	t.Setenv("PERSONA_NODEJS", "")
	systemDirs, distroDir := agentSystemDirs, distroNodeDir
	agentSystemDirs, distroNodeDir = []string{t.TempDir()}, t.TempDir()
	t.Cleanup(func() { agentSystemDirs, distroNodeDir = systemDirs, distroDir })
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	forgetCodexCatalogs(t)
}

func forgetCodexCatalogs(t *testing.T) {
	t.Helper()
	reset := func() {
		codexCatalogCache.Lock()
		codexCatalogCache.at, codexCatalogCache.url = map[string]time.Time{}, map[string]string{}
		codexCatalogCache.Unlock()
		codexVersionCache.Lock()
		codexVersionCache.stamp, codexVersionCache.version = "", ""
		codexVersionCache.Unlock()
		agentExecutableMu.Lock()
		delete(agentExecutableCache, "codex")
		agentExecutableMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// catalogGateway serves handler and records each request's path, query and Authorization.
type catalogGateway struct {
	*httptest.Server
	mu   sync.Mutex
	seen []*http.Request
}

func newCatalogGateway(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *catalogGateway {
	t.Helper()
	g := &catalogGateway{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.seen = append(g.seen, r.Clone(context.Background()))
		g.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *catalogGateway) requests() []*http.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]*http.Request(nil), g.seen...)
}

func codexProvider(base string) proto.ManagedLauncher {
	return proto.ManagedLauncher{Name: "gw", Agent: "codex", BaseURL: base, APIKey: "sk-test", Model: "example-big"}
}

func quietNode() *Node { return &Node{logger: log.New(io.Discard, "", 0)} }

// Each place a gateway may serve Codex's catalog is found by what it answers, the first in order winning; OpenAI's
// list, an error, a body that is no catalog or no answer in time is none.
func TestCodexCatalogFoundByShape(t *testing.T) {
	catalogAt := func(paths ...string) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			for _, p := range paths {
				if r.URL.Path == p && r.URL.Query().Get("client_version") != "" {
					io.WriteString(w, codexCatalogBody)
					return
				}
			}
			if r.URL.Path == "/v1/models" {
				io.WriteString(w, openAIModelsBody)
				return
			}
			http.NotFound(w, r)
		}
	}
	body := func(code int, s string) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, s)
		}
	}
	for _, c := range []struct {
		name    string
		handler func(w http.ResponseWriter, r *http.Request)
		want    string // the path below the server, "" for none
	}{
		{"models with client_version", catalogAt("/v1/models"), "/v1/models"},
		{"codex models", catalogAt("/v1/codex/models"), "/v1/codex/models"},
		{"both", catalogAt("/v1/codex/models", "/v1/models"), "/v1/models"},
		{"none", catalogAt(), ""},
		{"server error", body(http.StatusInternalServerError, codexCatalogBody), ""},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }, ""},
		{"not JSON", body(http.StatusOK, "<html>sign in</html>"), ""},
		{"empty", body(http.StatusOK, `{"models":[]}`), ""},
		{"duplicate slug", body(http.StatusOK, `{"models":[{"slug":"a","display_name":"A","visibility":"list","supported_reasoning_levels":[]},{"slug":"a","display_name":"A","visibility":"list","supported_reasoning_levels":[]}]}`), ""},
		{"no display name", body(http.StatusOK, `{"models":[{"slug":"a","visibility":"list","supported_reasoning_levels":[]}]}`), ""},
		{"levels not a list", body(http.StatusOK, `{"models":[{"slug":"a","display_name":"A","visibility":"list","supported_reasoning_levels":null}]}`), ""},
		{"nothing listed", body(http.StatusOK, `{"models":[{"slug":"a","display_name":"A","visibility":"hide","supported_reasoning_levels":[]}]}`), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeCodex(t, "0.167.0")
			g := newCatalogGateway(t, c.handler)
			got := quietNode().codexCatalogURL(context.Background(), codexProvider(g.URL+"/v1"))
			want := ""
			if c.want != "" {
				want = g.URL + c.want
			}
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			reqs := g.requests()
			if len(reqs) != len(codexCatalogPaths) {
				t.Fatalf("%d requests, want one per path", len(reqs))
			}
			for _, r := range reqs {
				if r.Method != http.MethodGet || r.URL.RawQuery != "client_version=0.167.0" || r.Header.Get("Authorization") != "Bearer sk-test" {
					t.Fatalf("asked %s %s with %q, want what codex asks", r.Method, r.URL, r.Header.Get("Authorization"))
				}
			}
		})
	}
	t.Run("no answer in time", func(t *testing.T) {
		fakeCodex(t, "0.167.0")
		old := codexCatalogTimeout
		codexCatalogTimeout = 200 * time.Millisecond
		t.Cleanup(func() { codexCatalogTimeout = old })
		release := make(chan struct{})
		g := newCatalogGateway(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		defer close(release)
		start := time.Now()
		if got := quietNode().codexCatalogURL(context.Background(), codexProvider(g.URL+"/v1")); got != "" {
			t.Fatalf("got %q", got)
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Fatalf("the probe held the start %s", took)
		}
	})
}

// No probe, and no catalog, where codex would not read one or the provider is not Persona's to describe.
func TestCodexCatalogNotProbed(t *testing.T) {
	for _, c := range []struct {
		name    string
		version string
		edit    func(*proto.ManagedLauncher)
	}{
		{"codex before catalogs by default", "0.160.0", nil},
		{"a prerelease of the first one", "0.161.0-alpha.2", nil},
		{"no codex", "", nil},
		{"no key", "0.167.0", func(l *proto.ManagedLauncher) { l.APIKey = "" }},
		{"key emptied by the provider's env", "0.167.0", func(l *proto.ManagedLauncher) { l.Env = "OPENAI_API_KEY=" }},
		{"its own provider", "0.167.0", func(l *proto.ManagedLauncher) { l.Args = []string{"-c", "model_provider=mine"} }},
		{"a profile", "0.167.0", func(l *proto.ManagedLauncher) { l.Args = []string{"--profile", "work"} }},
		{"a Claude provider", "0.167.0", func(l *proto.ManagedLauncher) { l.Agent = "" }},
		{"a base with a query", "0.167.0", func(l *proto.ManagedLauncher) { l.BaseURL += "?team=a" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeCodex(t, c.version)
			g := newCatalogGateway(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, codexCatalogBody) })
			l := codexProvider(g.URL + "/v1")
			if c.edit != nil {
				c.edit(&l)
			}
			if got := quietNode().codexCatalogURL(context.Background(), l); got != "" {
				t.Fatalf("got %q", got)
			}
			if reqs := g.requests(); len(reqs) != 0 {
				t.Fatalf("asked the gateway %s", reqs[0].URL)
			}
		})
	}
	t.Run("no gateway", func(t *testing.T) {
		fakeCodex(t, "0.167.0")
		if got := quietNode().codexCatalogURL(context.Background(), proto.ManagedLauncher{Name: "plain", Agent: "codex", APIKey: "k"}); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

// A probe's answer, a catalog or none, is kept for the same gateway, key and codex; another key asks again.
func TestCodexCatalogKept(t *testing.T) {
	fakeCodex(t, "0.169.0")
	g := newCatalogGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/codex/models" {
			io.WriteString(w, codexCatalogBody)
			return
		}
		io.WriteString(w, openAIModelsBody)
	})
	n := quietNode()
	l := codexProvider(g.URL + "/v1")
	for range 3 {
		if got := n.codexCatalogURL(context.Background(), l); got != g.URL+"/v1/codex/models" {
			t.Fatalf("got %q", got)
		}
	}
	if reqs := g.requests(); len(reqs) != len(codexCatalogPaths) {
		t.Fatalf("%d requests for three starts, want one probe", len(reqs))
	}
	if r := g.requests()[0]; r.URL.Query().Get("client_version") != "0.169.0" {
		t.Fatalf("client_version %q", r.URL.Query().Get("client_version"))
	}
	l.APIKey = "sk-other"
	n.codexCatalogURL(context.Background(), l)
	if reqs := g.requests(); len(reqs) != 2*len(codexCatalogPaths) {
		t.Fatalf("another key was answered from the first one's probe")
	}
}

// launcher.env carries the catalog URL to personad launch, and nothing when there is none.
func TestLauncherEnvCodexCatalog(t *testing.T) {
	fakeCodex(t, "0.167.0")
	with := newCatalogGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_version") != "" && r.URL.Path == "/v1/models" {
			io.WriteString(w, codexCatalogBody)
			return
		}
		io.WriteString(w, openAIModelsBody)
	})
	without := newCatalogGateway(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, openAIModelsBody) })
	n := quietNode()
	a, b := codexProvider(with.URL+"/v1"), codexProvider(without.URL+"/v1")
	a.Name, b.Name = "with", "without"
	n.setManaged([]proto.ManagedLauncher{a, b})
	if got := n.launcherEnv("with")["codex_catalog_url"]; got != with.URL+"/v1/models" {
		t.Fatalf("with a catalog: %v", got)
	}
	if got, ok := n.launcherEnv("without")["codex_catalog_url"]; ok {
		t.Fatalf("without a catalog: %v", got)
	}
}

func TestCodexVersionWords(t *testing.T) {
	for _, c := range []struct {
		out, version string
		reads        bool
	}{
		{"codex-cli 0.167.0\n", "0.167.0", true},
		{"codex-cli 0.161.0", "0.161.0", true},
		{"codex-cli 1.0.0", "1.0.0", true},
		{"codex-cli 0.160.9", "0.160.9", false},
		{"codex-cli 0.161.0-alpha.2", "0.161.0-", false},
		{"codex-cli 0.170.0-alpha.1", "0.170.0-", true},
		{"codex-cli 0.167.0+build.5", "0.167.0", true},
		{"codex-cli", "", false},
		{"", "", false},
		{"codex-cli 0.167", "", false},
		{"codex-cli v0.167.0", "", false},
		{"codex-cli 0.167.0\nnote: a newer version is out\n", "0.167.0", true},
	} {
		v := parseCodexVersion(c.out)
		if v != c.version || codexReadsCatalog(v) != c.reads {
			t.Errorf("%q: version %q reads %v, want %q %v", c.out, v, codexReadsCatalog(v), c.version, c.reads)
		}
	}
	if got := codexCatalogClientVersion("0.170.0-"); got != "0.170.0" {
		t.Errorf("client_version %q", got)
	}
	if strings.Contains(codexCatalogClientVersion("0.167.0"), "-") {
		t.Error("a release's client_version")
	}
}
