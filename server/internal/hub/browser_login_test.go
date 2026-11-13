package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func browserCall(t *testing.T, mux http.Handler, action, token string, body map[string]any, want int) map[string]any {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/browser-login/"+action, strings.NewReader(string(b)))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	status := response.Code
	if status != want {
		t.Fatalf("%s: status=%d want=%d result=%v", action, status, want, result)
	}
	return result
}

func TestBrowserLoginApproval(t *testing.T) {
	h := newTestHub(t)
	s := newPairStore()
	h.store = s
	mux := h.routes()
	_, phone := call(t, mux, "POST", "/api/v1/auth/tokens", h.cfg.Token, `{"name":"Phone","kind":"phone"}`)
	token := phone["token"].(string)
	c := browserCall(t, mux, "start", "", map[string]any{"name": "Test browser"}, 200)
	if strings.Contains(c["url"].(string), c["secret"].(string)) {
		t.Fatal("QR contains polling credential")
	}
	poll := map[string]any{"id": c["id"], "secret": c["secret"]}
	if browserCall(t, mux, "poll", "", poll, 200)["state"] != "pending" {
		t.Fatal("not pending")
	}
	approval := map[string]any{"id": c["id"], "hub": h.hubID()}
	browserCall(t, mux, "approve", "", approval, 401)
	browserCall(t, mux, "approve", h.cfg.Token, approval, 403)
	if browserCall(t, mux, "approve", token, approval, 200)["state"] != "scanned" {
		t.Fatal("not scanned")
	}
	approval["allow"] = true
	browserCall(t, mux, "approve", token, approval, 200)
	browserCall(t, mux, "poll", "", map[string]any{"id": c["id"], "secret": "wrong"}, 410)
	result := browserCall(t, mux, "poll", "", poll, 200)
	again := browserCall(t, mux, "poll", "", poll, 200)
	if result["token"] != again["token"] || result["id"] != again["id"] {
		t.Fatal("retry minted another device")
	}
	browserCall(t, mux, "approve", token, approval, 409)
	if code, _ := call(t, mux, "GET", "/api/v1/machines", result["token"].(string), ""); code != 200 {
		t.Fatalf("browser credential unusable: %d", code)
	}
}

func TestBrowserLoginDenialExpiryAndRevocation(t *testing.T) {
	for _, which := range []string{"denial", "expiry", "revocation", "other hub"} {
		t.Run(which, func(t *testing.T) {
			h := newTestHub(t)
			h.store = newPairStore()
			mux := h.routes()
			_, phone := call(t, mux, "POST", "/api/v1/auth/tokens", h.cfg.Token, `{"name":"Phone","kind":"phone"}`)
			token := phone["token"].(string)
			c := browserCall(t, mux, "start", "", map[string]any{"name": "Test browser"}, 200)
			approval := map[string]any{"id": c["id"], "hub": h.hubID(), "allow": true}
			poll := map[string]any{"id": c["id"], "secret": c["secret"]}
			switch which {
			case "denial":
				approval["allow"] = false
				browserCall(t, mux, "approve", token, approval, 200)
				if browserCall(t, mux, "poll", "", poll, 200)["state"] != "denied" {
					t.Fatal("not denied")
				}
			case "expiry":
				h.browserLogin.flows[c["id"].(string)].Until = time.Now().Add(-time.Second)
				browserCall(t, mux, "approve", token, approval, 410)
				browserCall(t, mux, "poll", "", poll, 410)
			case "revocation":
				browserCall(t, mux, "approve", token, approval, 200)
				call(t, mux, "DELETE", "/api/v1/auth/tokens/"+phone["id"].(string), h.cfg.Token, "")
				browserCall(t, mux, "poll", "", poll, 403)
			case "other hub":
				approval["hub"] = "other"
				browserCall(t, mux, "approve", token, approval, 400)
			}
		})
	}
}

func TestBrowserLoginRejectsCrossOriginAndPlaintext(t *testing.T) {
	h := newTestHub(t)
	h.store = newPairStore()
	mux := h.routes()
	r := securityRequest(mux, "POST", "/api/v1/auth/browser-login/start", "", `{"name":"Browser"}`, "203.0.113.1:1", false)
	if r.Code != 403 {
		t.Fatalf("plaintext accepted: %d", r.Code)
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/browser-login/start", strings.NewReader(`{"name":"Browser"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != 403 {
		t.Fatalf("cross-origin request accepted: %d", response.Code)
	}

}
