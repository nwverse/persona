package hub

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
)

func TestWhitespaceFileEditPathsPassThrough(t *testing.T) {
	var calls []map[string]any
	mux := fileEditHub(t, func(_ string, p map[string]any) (any, error) {
		calls = append(calls, p)
		return map[string]any{"ok": true}, nil
	})
	for _, c := range []struct{ op, body string }{
		{"rename", `{"path":" ","cwd":"/project","name":"result "}`},
		{"move", `{"paths":["source "],"cwd":"/project","dir":" "}`},
		{"trash", `{"paths":[" ","source "]}`},
	} {
		if code, _ := postEdit(mux, c.op, c.body); code != 200 {
			t.Fatalf("%s: status %d", c.op, code)
		}
	}
	if calls[0]["path"] != " " || calls[0]["name"] != "result " {
		t.Fatalf("rename changed: %+v", calls[0])
	}
	if calls[1]["dir"] != "\u00a0" || calls[1]["paths"].([]any)[0] != "source\u00a0" {
		t.Fatalf("move changed: %+v", calls[1])
	}
	if calls[2]["paths"].([]any)[0] != " " {
		t.Fatalf("trash changed: %+v", calls[2])
	}
	if code, _ := postEdit(mux, "restore", `{"trash_ids":[" "]}`); code != 400 {
		t.Fatalf("blank trash id: %d", code)
	}
}

func TestWhitespaceUploadTargetPassesThrough(t *testing.T) {
	f := &uploadNode{}
	_, machine, mux := uploadHubOf(t, f)
	machine.mu.Lock()
	machine.info.Capabilities = []string{nodeproto.ExactFilePaths}
	machine.mu.Unlock()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/machines/m1/files?name=report%20&dest=dir&dir=%C2%A0&cwd=%2Fproject", strings.NewReader("hi")))
	if rec.Code != 200 || len(f.begins) != 1 {
		t.Fatalf("upload: %d %s %+v", rec.Code, rec.Body, f.begins)
	}
	if f.begins[0]["name"] != "report " || f.begins[0]["dir"] != "\u00a0" {
		t.Fatalf("upload changed: %+v", f.begins[0])
	}
}

func TestWhitespaceUploadRefusesOlderNode(t *testing.T) {
	f := &uploadNode{}
	_, machine, mux := uploadHubOf(t, f)
	request := func(name string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/machines/m1/files?name="+name+"&dest=dir&dir=%2Fproject", strings.NewReader("hi")))
		return rec
	}
	if rec := request("report%20"); rec.Code != 409 || len(f.begins) != 0 {
		t.Fatalf("old node: %d %s %+v", rec.Code, rec.Body, f.begins)
	}
	if rec := request("ordinary"); rec.Code != 200 || len(f.begins) != 1 {
		t.Fatalf("ordinary path: %d %s %+v", rec.Code, rec.Body, f.begins)
	}
	machine.mu.Lock()
	machine.info.Capabilities = []string{nodeproto.ExactFilePaths}
	machine.mu.Unlock()
	if rec := request("report%20"); rec.Code != 200 || len(f.begins) != 2 || f.begins[1]["name"] != "report " {
		t.Fatalf("new node: %d %s %+v", rec.Code, rec.Body, f.begins)
	}
}

func TestWhitespaceDownloadAndThumbnailPathsPassThrough(t *testing.T) {
	f := &downloadNode{}
	mux := downloadHub(t, f)
	for _, path := range []string{"/api/v1/machines/m1/file/download?path=%20&cwd=%2Fproject", "/api/v1/machines/m1/file/thumb?path=%C2%A0&cwd=%2Fproject"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if len(f.requests) == 0 || f.requests[0].Path != " " {
		t.Fatalf("download: %+v", f.requests)
	}
	if len(f.thumbs) != 1 || f.thumbs[0].Path != "\u00a0" {
		t.Fatalf("thumbnail: %+v", f.thumbs)
	}
}
