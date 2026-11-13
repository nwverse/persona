package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// Environment is what the hub gives a program in its environment (docs/extensions/extensions.md "How config and secrets reach the extension").
type Environment struct {
	ID, Dir, Data, Socket string
	// Config and Secrets are for a program that does not speak the protocol (rpc: none): the protocol hands them
	// over with initialize instead.
	Config  json.RawMessage
	Secrets map[string]string
	// HubURL and HubToken: only with the hub_api permission accepted.
	HubURL, HubToken string
}

// Env reads the environment, under the names of any hub: PERSONA_EXTENSION_*, before the rename (#1844)
// PERSONA_PLUGIN_*, before Persona PERSONA_PLUGIN_*.
func Env() Environment {
	e := Environment{
		ID: getenv("ID"), Dir: getenv("DIR"), Data: getenv("DATA"), Socket: getenv("SOCKET"),
		HubURL: hubEnv("URL"), HubToken: hubEnv("TOKEN"),
	}
	if c := getenv("CONFIG"); c != "" {
		e.Config = json.RawMessage(c)
	}
	if s := getenv("SECRETS"); s != "" {
		_ = json.Unmarshal([]byte(s), &e.Secrets)
	}
	return e
}

func getenv(name string) string {
	return extensionEnv(name)
}

func first(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// httpDrain is how long ServeHTTP waits for the requests in flight once its ctx ends. It fits in the grace the hub
// gives a program it stops (5 seconds after closing stdin, and again after SIGTERM). A variable for the tests.
var httpDrain = 5 * time.Second

// ServeHTTP serves h on the unix socket at path (the hub forwards /api/v1/extensions/<id>/… and an api
// contribution's prefix to it) until ctx ends. GET /health, which the hub polls, is answered here.
//
// When ctx ends it stops accepting connections and returns once the requests in flight are answered; requests
// still running after 5 seconds are cut, and the error says so.
func ServeHTTP(ctx context.Context, path string, h http.Handler) error {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 30 * time.Second}
	failed := make(chan struct{})
	drained := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-failed:
			return
		}
		sctx, cancel := context.WithTimeout(context.Background(), httpDrain)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			_ = srv.Close()
			drained <- fmt.Errorf("http: cut the requests still running after %s: %w", httpDrain, err)
			return
		}
		drained <- nil
	}()
	// Serve returns as soon as Shutdown begins, with requests still in flight: wait for Shutdown as well.
	if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		close(failed)
		_ = srv.Close()
		return err
	}
	return <-drained
}
