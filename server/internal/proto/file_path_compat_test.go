package proto

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestNeedsExactFilePaths(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString([]byte(`{"n":"item","r":"/project/dest /item"}`))
	for _, tc := range []struct {
		method string
		params any
		want   bool
	}{
		{"file.read", map[string]any{"path": "~/report "}, true},
		{"file.rename", map[string]any{"path": "~/report /"}, true},
		{"file.rename", map[string]any{"path": "~/report", "name": "new "}, false},
		{"file.move", map[string]any{"paths": []string{"a"}, "dir": "dest\u00a0"}, true},
		{"file.trash", map[string]any{"paths": []string{"a", "b "}}, true},
		{"fs.stat", map[string]any{"paths": []string{" "}}, true},
		{"fs.list", map[string]any{"path": "", "cwd": "/project "}, false},
		{"fs.find", map[string]any{"cwd": "/project "}, true},
		{"fs.watch", map[string]any{"dirs": []map[string]string{{"path": "~/dest "}}}, true},
		{"file.upload.begin", map[string]any{"dest": "dir", "dir": "~/dest", "name": "report "}, true},
		{"file.upload.begin", map[string]any{"dest": "terminal", "name": "report "}, false},
		{"file.restore", map[string]any{"trash_ids": []string{id}}, true},
		{"workspace.resolve", json.RawMessage(`{"path":"/project "}`), true},
		{"file.read", map[string]any{"path": "~/dir /report"}, false},
		{"session.send", map[string]any{"path": "unrelated "}, false},
	} {
		if got := NeedsExactFilePaths(tc.method, tc.params, false); got != tc.want {
			t.Errorf("%s %+v: got %v, want %v", tc.method, tc.params, got, tc.want)
		}
	}
	if !NeedsExactFilePaths("file.read", map[string]any{"path": `C:\work \item`}, true) {
		t.Fatal("Windows intermediate component was accepted")
	}
	if !NeedsExactFilePaths("file.move", map[string]any{"paths": []string{"item"}, "dir": "dest", "cwd": `C:\work `}, true) {
		t.Fatal("Windows cwd was accepted")
	}
}
