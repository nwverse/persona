package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/update"
)

// The external entry point, not just a constructed transport: current and legacy names reach a real self-signed TLS
// server through Main; no fingerprint preserves ordinary system-CA verification. A request cancels the test manager,
// so the test never needs a working hub or waits through the bus's reconnect backoff.
func TestExternalAssistantFingerprintEnvironment(t *testing.T) {
	database := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires an isolated test Postgres")
	}
	for _, mode := range []string{"current", "legacy", "current wins", "system"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			data := filepath.Join(home, ".persona")
			if err := os.MkdirAll(data, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, "hub.json"), []byte(`{"database_url":"postgres://u@127.0.0.1:1/x"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("PERSONA_HOME", data)
			t.Setenv("PERSONA_ASSISTANT_DEPLOY", "external")
			t.Setenv("PERSONA_ASSISTANT_TOKEN", "test-token")
			t.Setenv("PERSONA_DATABASE_URL", database)
			// Explicitly empty current values have precedence over legacy ones, as for every other Persona setting.
			t.Setenv("PERSONA_ASSISTANT_FINGERPRINT", "")
			t.Setenv("JARVIS_ASSISTANT_FINGERPRINT", "")

			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			reached := false
			var logs syncBuffer
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				stop()
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"` + bus.CodeTokenRevoked + `"}`))
			}))
			defer srv.Close()
			srv.Config.ErrorLog = log.New(&logs, "", 0)
			t.Setenv("PERSONA_ASSISTANT_HUB", srv.URL)
			sum := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
			fingerprint := base64.RawURLEncoding.EncodeToString(sum[:])
			switch mode {
			case "current":
				t.Setenv("PERSONA_ASSISTANT_FINGERPRINT", fingerprint)
			case "legacy":
				os.Unsetenv("PERSONA_ASSISTANT_FINGERPRINT")
				t.Setenv("JARVIS_ASSISTANT_FINGERPRINT", fingerprint)
			case "current wins":
				t.Setenv("PERSONA_ASSISTANT_FINGERPRINT", fingerprint)
				t.Setenv("JARVIS_ASSISTANT_FINGERPRINT", "wrong-pin")
			}

			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err := Main(ctx, update.Build{}, nil)
			if mode == "system" {
				// Run retries TLS errors with a backoff. The server's TLS error log is authoritative:
				// the client rejected the self-signed certificate before sending any HTTP request.
				if reached || !strings.Contains(logs.String(), "bad certificate") {
					t.Fatalf("unpinned request reached=%v, TLS error=%q, error=%v", reached, logs.String(), err)
				}
			} else if !reached {
				t.Fatalf("pinned connection did not reach the hub: %v", err)
			}
		})
	}
}

type syncBuffer struct {
	sync.Mutex
	strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Builder.Write(p)
}

func (b *syncBuffer) String() string {
	b.Lock()
	defer b.Unlock()
	return b.Builder.String()
}
