package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirectAPIEndpointWithVersionedBase(t *testing.T) {
	for _, api := range []string{"messages", "responses"} {
		t.Run(api, func(t *testing.T) {
			var path string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				if api == "responses" {
					_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
				} else {
					_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
				}
			}))
			defer server.Close()
			client := New(Config{API: api, BaseURL: server.URL + "/prefix/v1/", APIKey: "key", Model: "model"})
			text, err := client.Complete(context.Background(), "system", "hello", 10)
			if err != nil || text != "ok" || path != "/prefix/v1/"+api {
				t.Fatalf("text=%q err=%v path=%s", text, err, path)
			}
		})
	}
}
