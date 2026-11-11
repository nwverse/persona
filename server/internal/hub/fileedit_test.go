package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// fileEditHub serves the four routes over a real rpc pair whose node answers with answer.
func fileEditHub(t *testing.T, answer func(method string, params map[string]any) (any, error)) http.Handler {
	t.Helper()
	h := newTestHub(t)
	toNode, nodeIn := io.Pipe()
	toHub, hubIn := io.Pipe()
	hubPeer := rpc.New("h", nodeIn)
	nodePeer := rpc.New("n", hubIn)
	nodePeer.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		return answer(method, p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); toNode.Close(); toHub.Close() })
	go hubPeer.Serve(ctx, toHub)
	go nodePeer.Serve(ctx, toNode)
	m := newMachine(h, MachineConfig{ID: "m1", Name: "box"})
	m.peer = hubPeer
	m.info.Capabilities = []string{nodeproto.ExactFilePaths}
	h.byID["m1"] = m
	mux := http.NewServeMux()
	for _, op := range []string{"rename", "move", "trash", "restore"} {
		mux.HandleFunc("POST /api/v1/machines/{mid}/files/"+op, h.fileEdit(op))
	}
	return mux
}

func postEdit(mux http.Handler, op, body string) (int, map[string]any) {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/machines/m1/files/"+op, strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// Each route reaches the node's file.<op> with the body's fields and returns its answer as it is.
func TestFileEditPassesThrough(t *testing.T) {
	var got []string
	mux := fileEditHub(t, func(method string, p map[string]any) (any, error) {
		b, _ := json.Marshal(p)
		got = append(got, method+" "+string(b))
		return map[string]any{"ok": method}, nil
	})
	for _, c := range []struct{ op, body string }{
		{"rename", `{"path":"~/a.txt","cwd":"/w","name":"b.txt"}`},
		{"move", `{"paths":["~/a","~/b"],"cwd":"/w","dir":"~/d","conflict":"keep"}`},
		{"trash", `{"paths":["~/a"],"cwd":"/w"}`},
		{"restore", `{"trash_ids":["t1"]}`},
	} {
		if code, out := postEdit(mux, c.op, c.body); code != 200 || out["ok"] != "file."+c.op {
			t.Fatalf("%s: %d %v", c.op, code, out)
		}
	}
	want := []string{
		`file.rename {"cwd":"/w","name":"b.txt","path":"~/a.txt"}`,
		`file.move {"conflict":"keep","cwd":"/w","dir":"~/d","paths":["~/a","~/b"]}`,
		`file.trash {"cwd":"/w","paths":["~/a"]}`,
		`file.restore {"trash_ids":["t1"]}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("node got:\n%s", strings.Join(got, "\n"))
	}
}

// What can't be a request is 400 before the node hears of it.
func TestFileEditRefusesBadRequests(t *testing.T) {
	mux := fileEditHub(t, func(method string, _ map[string]any) (any, error) {
		t.Errorf("%s reached the node", method)
		return nil, nil
	})
	for _, c := range []struct{ op, body string }{
		{"rename", `{"name":"b"}`},
		{"rename", `{"path":"~/a","name":"x/y"}`},
		{"rename", `{"path":"~/a","name":".."}`},
		{"move", `{"paths":["~/a"]}`},
		{"move", `{"paths":[],"dir":"~"}`},
		{"move", `{"paths":["~/a"],"dir":"~","conflict":"overwrite"}`},
		{"trash", `{"paths":[""]}`},
		{"restore", `{}`},
		{"trash", `not json`},
	} {
		if code, _ := postEdit(mux, c.op, c.body); code != 400 {
			t.Errorf("%s %s: %d", c.op, c.body, code)
		}
	}
}

// The node's refusals come out as the app acts on them: read_only and exists (with the name) 409, not_found 404,
// a node from before these 409 node_outdated, anything else 502.
func TestFileEditErrors(t *testing.T) {
	var fail error
	mux := fileEditHub(t, func(string, map[string]any) (any, error) { return nil, fail })
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{&rpc.Error{Code: proto.CodeReadOnly, Message: "read-only"}, 409, "read_only"},
		{&rpc.Error{Code: proto.CodeExists, Message: "taken", Data: json.RawMessage(`{"name":"a.txt"}`)}, 409, "exists"},
		{&rpc.Error{Code: proto.CodeNotFound, Message: "~/a is not there"}, 404, "not_found"},
		{errors.New("unknown method file.trash"), 409, "node_outdated"},
		{errors.New("~/a is on another disk"), 502, "internal"},
	} {
		fail = c.err
		code, out := postEdit(mux, "trash", `{"paths":["~/a"]}`)
		if code != c.status || out["code"] != c.code {
			t.Errorf("%v: %d %v", c.err, code, out)
		}
		if c.code == "exists" && out["name"] != "a.txt" {
			t.Errorf("exists without the name: %v", out)
		}
	}
}
