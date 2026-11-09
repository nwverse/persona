package hub

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAndroidAppRoutes(t *testing.T) {
	h := newTestHub(t)
	h.store = newPairStore()
	token := "jp_android-test"
	if err := h.store.CreatePairToken(context.Background(), hashToken(token), time.Now().Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	handler := h.routes()
	request := func(path, credential, byteRange string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/app/android/"+path, nil)
		r.RemoteAddr = net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), "1")
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		r.Header.Set("Range", byteRange)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"latest", "apk"} {
		if w := request(path, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
		w := request(path, token, "")
		if w.Code != http.StatusNotFound || errorResponse(t, w)["code"] != "not_found" {
			t.Fatalf("missing %s: %d %s", path, w.Code, w.Body)
		}
	}
	apk := []byte("0123456789")
	sum := sha256.Sum256(apk)
	digest := hex.EncodeToString(sum[:])
	manifest, err := json.Marshal(map[string]any{
		"version_code": 42, "version_name": "1.2", "sha256": digest,
		"size": len(apk), "min_sdk": 26, "notes": "更新说明",
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.cfg.dir, "apps", "android")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"latest.json": manifest, digest + ".apk": apk} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if w := request("latest", token, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "apk?sha256="+digest) {
		t.Fatalf("latest: %d %s", w.Code, w.Body)
	}

	// A cached manifest must keep downloading its own APK after a newer publication.
	newer := bytes.Replace(manifest, []byte(digest), []byte(strings.Repeat("a", 64)), 1)
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if w := request("apk?sha256="+digest, token, ""); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), apk) {
		t.Fatalf("pinned download after publication: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"../latest.json", strings.Repeat("g", 64), "abc"} {
		if w := request("apk?sha256="+url.QueryEscape(bad), token, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid digest: %d", w.Code)
		}
	}
	if w := request("apk?sha256="+strings.Repeat("b", 64), token, ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing pinned APK: %d", w.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	w := request("apk?token="+url.QueryEscape(token), "", "bytes=2-5")
	if w.Code != http.StatusPartialContent || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("range: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w.Header().Get("Content-Type") != "application/vnd.android.package-archive" {
		t.Fatalf("APK content type: %v", w.Header())
	}
}

// The publisher writes <sha256>.apk.gz next to the APK; a download that takes gzip gets it, every other one the APK as
// before, with the same headers.
func TestAndroidAPKGzipCopy(t *testing.T) {
	h := newTestHub(t)
	h.store = newPairStore()
	token := "jp_android-gzip"
	if err := h.store.CreatePairToken(context.Background(), hashToken(token), time.Now().Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	handler := h.routes()
	apk := bytes.Repeat([]byte("stored dex bytes "), 4096)
	sum := sha256.Sum256(apk)
	digest := hex.EncodeToString(sum[:])
	dir := filepath.Join(h.cfg.dir, "apps", "android")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"version_code": 42, "sha256": digest, "size": len(apk)})
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, digest+".apk"), apk, 0o600); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, headers ...string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/app/android/"+path, nil)
		r.RemoteAddr = net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), "1")
		r.Header.Set("Authorization", "Bearer "+token)
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	identity := func(label string, w *httptest.ResponseRecorder, want []byte) {
		t.Helper()
		if !bytes.Equal(w.Body.Bytes(), want) || w.Header().Get("Content-Encoding") != "" || w.Header().Get("Vary") != "" ||
			w.Header().Get("ETag") != `"`+digest+`"` || w.Header().Get("Content-Type") != "application/vnd.android.package-archive" ||
			w.Header().Get("Content-Disposition") != `attachment; filename="persona.apk"` {
			t.Fatalf("%s: %d %v (%d bytes)", label, w.Code, w.Header(), w.Body.Len())
		}
	}
	gz := "Accept-Encoding"
	identity("no .gz yet", request(http.MethodGet, "apk", gz, "gzip"), apk)

	var packed bytes.Buffer
	zw := gzip.NewWriter(&packed)
	_, _ = zw.Write(apk)
	_ = zw.Close()
	if err := os.WriteFile(filepath.Join(dir, digest+".apk.gz"), packed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"apk", "apk?sha256=" + digest, "apk?token=" + url.QueryEscape(token)} {
		w := request(http.MethodGet, path, gz, "gzip, deflate, br")
		zr, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatalf("%s: %d %v: %v", path, w.Code, w.Header(), err)
		}
		plain, err := io.ReadAll(zr)
		decoded := sha256.Sum256(plain)
		if err != nil || hex.EncodeToString(decoded[:]) != digest || w.Code != http.StatusOK ||
			w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" ||
			w.Header().Get("ETag") != `"`+digest+`-gzip"` || w.Header().Get("Content-Length") != strconv.Itoa(packed.Len()) ||
			w.Header().Get("Content-Type") != "application/vnd.android.package-archive" ||
			w.Header().Get("Content-Disposition") != `attachment; filename="persona.apk"` {
			t.Fatalf("%s gzip: %d %v %v", path, w.Code, w.Header(), err)
		}
	}
	if w := request(http.MethodGet, "apk", gz, "gzip", "If-None-Match", `"`+digest+`-gzip"`); w.Code != http.StatusNotModified {
		t.Fatalf("revalidated gzip copy: %d", w.Code)
	}
	// Not taking gzip, HEAD and ranges keep getting the APK itself.
	identity("identity", request(http.MethodGet, "apk"), apk)
	identity("gzip refused", request(http.MethodGet, "apk", gz, "gzip;q=0"), apk)
	identity("gzip tag, no gzip", request(http.MethodGet, "apk", gz, "gzip", "If-None-Match", `"`+digest+`-gzip"`, gz, "identity"), apk)
	if w := request(http.MethodHead, "apk", gz, "gzip"); w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "" ||
		w.Header().Get("Content-Length") != strconv.Itoa(len(apk)) {
		t.Fatalf("HEAD: %d %v", w.Code, w.Header())
	}
	if w := request(http.MethodGet, "apk", gz, "gzip", "Range", "bytes=2-5"); w.Code != http.StatusPartialContent ||
		w.Body.String() != string(apk[2:6]) || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("range: %d %v %q", w.Code, w.Header(), w.Body)
	}
}

func TestAndroidPairDownloadLink(t *testing.T) {
	p := pairCode{URL: "http://example.com", Token: "jp_example+&", ExpiresAt: time.Now().Add(time.Minute)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(p)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	cfg := &Config{Listen: ":" + port, PublicURL: p.URL + "/", Token: "master-example"}
	var out bytes.Buffer
	if err := cfg.printPair(context.Background(), false, &out, true); err != nil {
		t.Fatal(err)
	}
	want := p.URL + "/api/v1/app/android/apk?token=" + url.QueryEscape(p.Token)
	if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), p.pairURL()) || strings.Contains(out.String(), cfg.Token) {
		t.Fatal("Android link must share the temporary pairing credential")
	}
	out.Reset()
	if err := cfg.printPair(context.Background(), true, &out, true); err == nil || out.Len() != 0 {
		t.Fatal("Android legacy pairing must not expose the master credential")
	}
}
