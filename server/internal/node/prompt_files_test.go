package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptFilesBindExecutionAndFailBeforeDelivery(t *testing.T) {
	ctx := context.WithValue(t.Context(), taskInputKey{}, true)
	f := cliTestBash(t)
	f.n.machineID = "box"
	cwd := t.TempDir()
	file := filepath.Join(cwd, "notes.txt")
	if err := os.WriteFile(file, []byte("current content"), 0600); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf(`<persona-context kind="file" path="notes.txt" machine="%s" cwd="%s" launcher="claude" runtime="%s">Notes</persona-context>`+"\n\nRead it", f.n.machineID, cwd, "")
	if _, err := f.n.selectedFiles(t.Context(), text, cwd, "claude", ""); err == nil {
		t.Fatal("unmarked conversation read a task selection")
	}
	outside := filepath.Join(t.TempDir(), "out.txt")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(cwd, "outside-link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, "outside-link"} {
		if _, err := f.n.selectedFiles(ctx, strings.Replace(text, "notes.txt", path, 1), cwd, "claude", ""); err == nil {
			t.Fatal("selection crossed task directory")
		}
	}
	entries, err := nodeCall(t, f.n, "fs.task.list", map[string]string{"cwd": cwd, "path": "."})
	if err != nil || !strings.Contains(string(entries), "notes.txt") || strings.Contains(string(entries), "outside-link") {
		t.Fatalf("task picker: %s %v", entries, err)
	}
	if _, err := nodeCall(t, f.n, "fs.task.list", map[string]string{"cwd": cwd, "path": filepath.Dir(outside)}); err == nil {
		t.Fatal("outside directory listed")
	}
	physical, _ := filepath.EvalSymlinks(file)
	selected, err := f.n.selectedFiles(ctx, text, cwd, "claude", "")
	if err != nil || len(selected) != 1 || selected[0].Path != physical {
		t.Fatalf("resolve: %+v %v", selected, err)
	}
	for _, changed := range []string{strings.Replace(text, `launcher="claude"`, `launcher="other"`, 1), strings.Replace(text, `cwd="`+cwd+`"`, `cwd="/another"`, 1), strings.Replace(text, `machine="`+f.n.machineID+`"`, `machine="other"`, 1)} {
		if _, err := f.n.assistantSend(ctx, "", cwd, "claude", changed, nil, "default", "", modelExecution{}); err == nil {
			t.Fatal("stale selection delivered")
		}
	}
	wire := map[string]any{"text": text, "task_input": true, "cwd": cwd, "launcher": "claude", "permission_mode": "default", "origin": "task", "origin_known": true}
	reply, err := nodeCall(t, f.n, "assistant.send", wire)
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		ID string `json:"session_id"`
	}
	if err := json.Unmarshal(reply, &started); err != nil || started.ID == "" {
		t.Fatalf("start: %s %v", reply, err)
	}
	id := started.ID
	f.idle(t, id)
	if got := f.file("stdin.jsonl"); !strings.Contains(got, "file-instructions") || !strings.Contains(got, physical) {
		t.Fatalf("file not injected: %s", got)
	}
	// Content may change between runs; the reference stays at the same path, without storing a snapshot.
	if err := os.WriteFile(file, []byte("updated content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.selectedFiles(ctx, text, cwd, "claude", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	before := f.file("stdin.jsonl")
	if _, err := f.n.assistantSend(ctx, id, cwd, "claude", text, nil, "default", "", modelExecution{}); err == nil {
		t.Fatal("deleted file delivered")
	}
	if f.file("stdin.jsonl") != before {
		t.Fatal("sent after file refusal")
	}
}
