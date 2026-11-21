package link

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCloudflareAccessPairControlAndStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := make(chan string, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != "device" || r.Header.Get("CF-Access-Client-Secret") != "sample" {
			http.Error(w, "Access refused", 403)
			return
		}
		if r.URL.Path == "/api/v1/auth/tokens" && r.Header.Get("Authorization") == "Bearer jp_test" {
			fmt.Fprint(w, `{"token":"jd_test","role":"link"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer jd_test" {
			http.Error(w, "Hub refused", 401)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		_, data, err := ws.Read(ctx)
		if r.URL.Path == StreamPath && err == nil {
			stream <- string(data)
		}
	}))
	defer srv.Close()
	testCA(t, srv)
	path := filepath.Join(t.TempDir(), "link.json")
	if err := SetCloudflareAccess(path, &CloudflareAccess{srv.URL, "device", "sample"}); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"hub": {"test"}, "machine": {"mac"}, "token": {"jp_test"}, "url": {srv.URL}}
	if _, err := Pair(ctx, "persona://link?"+q.Encode(), "", "mac", path); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := dial(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer l.closeNow()
	if err = l.forward(ctx, "stream-1", newFakeNode(t).path); err != nil {
		t.Fatal(err)
	}
	select {
	case greeting := <-stream:
		if greeting != nodeHello {
			t.Fatal("unexpected node greeting")
		}
	case <-ctx.Done():
		t.Fatal("no forwarded bytes")
	}
}

func TestAccessTokenStaysOnItsOrigin(t *testing.T) {
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Secret") != "" || r.Header.Get("CF-Access-Client-Id") != "" {
			t.Error("credentials sent to another origin")
		}
	}))
	defer other.Close()
	testCA(t, other)
	c := Config{Trust: TrustSystem, CloudflareAccess: &CloudflareAccess{"https://127.0.0.1:1", "device", "sample"}}
	resp, err := hubClient(&c, nil, nil).Get(other.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
