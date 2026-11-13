package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/mcpwait"
)

func TestLCUElicitationIsAnsweredOnTheCardAndNeverImplied(t *testing.T) {
	params := []byte(`{"mode":"form","message":"Allow Calculator?","requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call","connector_id":"computer-use","tool_params":{"app":"com.apple.calculator"},"tool_params_display":[{"name":"app","value":"Calculator"}],"persist":["session"]}}`)
	asked := make(chan struct{}, 1)
	sess := newLCUSession(func(ctx context.Context, conv string, raw json.RawMessage) (map[string]any, error) {
		if conv != "conv-1" {
			t.Errorf("conversation %q", conv)
		}
		if callID := mcpwait.CallFrom(ctx); callID != "call-lcu" {
			t.Errorf("MCP call ID %q", callID)
		}
		if origin := mcpwait.MachineFrom(ctx); origin != "origin" {
			t.Errorf("MCP origin %q", origin)
		}
		if string(raw) == "" {
			t.Error("empty elicitation")
		}
		asked <- struct{}{}
		return map[string]any{"action": "accept", "content": map[string]any{}, "_meta": map[string]any{"persist": "session"}}, nil
	})
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	sess.attachPipes(clientRead, clientWrite)
	errc := make(chan error, 1)
	go func() { errc <- lcuFakeServer(serverRead, serverWrite, params) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = mcpwait.WithMachine(mcpwait.WithCall(ctx, "call-lcu"), "origin")
	res, err := sess.call(ctx, "js", map[string]any{"code": "return 1"}, "persona-abcd", "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"ok":true}` || res.Image == nil || res.Image.Mime != "image/png" || res.Image.Data != "AAAA" {
		t.Fatalf("%+v", res)
	}
	select {
	case <-asked:
	default:
		t.Fatal("the elicitation was not shown")
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}

	// No asker: the runtime is cancelled, not accepted.
	silent := newLCUSession(nil)
	serverRead, clientWrite = io.Pipe()
	clientRead, serverWrite = io.Pipe()
	silent.attachPipes(clientRead, clientWrite)
	errc = make(chan error, 1)
	go func() { errc <- lcuFakeServer(serverRead, serverWrite, params) }()
	if _, err := silent.call(ctx, "js", map[string]any{"code": "return 1"}, "persona-abcd", "conv-1"); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("no asker must not succeed: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeElicitCancelsAnythingButARealAnswer(t *testing.T) {
	if got := normalizeElicit(map[string]any{"action": "approve"}); got["action"] != "cancel" {
		t.Fatalf("%v", got)
	}
	if got := normalizeElicit(map[string]any{"action": "decline"}); got["action"] != "decline" {
		t.Fatalf("%v", got)
	}
}

func TestLCUToolTranslation(t *testing.T) {
	name, args, err := lcuTool("run_script", json.RawMessage(`{"script":"return 1","timeout_ms":1000}`))
	if err != nil || name != "js" || args["code"] != "return 1" || args["timeout_ms"] != float64(1000) {
		t.Fatalf("%s %v %v", name, args, err)
	}
	name, args, err = lcuTool("run_steps", json.RawMessage(`{"app":"Notes","steps":[{"press_key":{"key":"return"}}]}`))
	if err != nil || name != "js" || !strings.Contains(args["code"].(string), `pressKey("return")`) || !strings.Contains(args["code"].(string), `appNamed("Notes")`) {
		t.Fatalf("%s %v %v", name, args, err)
	}
	// args null is no args: an object for LCU's tools/call, not "arguments": null.
	if name, args, err := lcuTool("js", json.RawMessage(`null`)); err != nil || name != "js" || args == nil {
		t.Fatalf("args null: %s %v %v", name, args, err)
	}
	if _, _, err := lcuTool("clipboard_read", json.RawMessage(`{}`)); err == nil {
		t.Fatal("clipboard is not forwarded")
	}
}

func TestLCUChildEnvDropsSecrets(t *testing.T) {
	t.Setenv("PERSONA_TEST_TOKEN", "secret")
	for _, e := range lcuChildEnv() {
		if strings.HasPrefix(e, "PERSONA_TEST_TOKEN=") {
			t.Fatal("token in the LCU environment")
		}
	}
}

// lcuFakeServer is one tools/call: it elicits, requires the answer not be an unsolicited accept, then returns a result.
// When the answer is cancel it returns the runtime's own "not approved" error.
func lcuFakeServer(r io.Reader, w io.Writer, elicit json.RawMessage) error {
	br := bufio.NewReader(r)
	bw := bufio.NewWriter(w)
	raw, err := readMCP(br)
	if err != nil {
		return err
	}
	var call mcpMessage
	if json.Unmarshal(raw, &call) != nil || call.Method != "tools/call" || call.ID == nil {
		return errString("expected tools/call")
	}
	var meta struct {
		Meta struct {
			Turn struct {
				SessionID string `json:"session_id"`
				TurnID    string `json:"turn_id"`
			} `json:"x-codex-turn-metadata"`
		} `json:"_meta"`
	}
	if json.Unmarshal(call.Params, &meta) != nil || meta.Meta.Turn.SessionID != "persona-abcd" || meta.Meta.Turn.TurnID == "" {
		return errString("missing turn metadata")
	}
	id := int64(99)
	frame, _ := json.Marshal(mcpMessage{JSONRPC: "2.0", ID: &id, Method: "elicitation/create", Params: elicit})
	if _, err := bw.WriteString(string(frame) + "\n"); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	ansRaw, err := readMCP(br)
	if err != nil {
		return err
	}
	var ans mcpMessage
	if json.Unmarshal(ansRaw, &ans) != nil || ans.ID == nil || *ans.ID != id {
		return errString("elicitation answer was not for this request")
	}
	var result struct {
		Action string         `json:"action"`
		Meta   map[string]any `json:"_meta"`
	}
	if json.Unmarshal(ans.Result, &result) != nil {
		return errString("bad elicitation result")
	}
	if result.Action == "accept" {
		if result.Meta["persist"] != "session" {
			return errString("accept without the user's session choice")
		}
		body, _ := json.Marshal(map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": `{"ok":true}`},
				map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"},
			},
		})
		done, _ := json.Marshal(mcpMessage{JSONRPC: "2.0", ID: call.ID, Result: body})
		_, err = bw.WriteString(string(done) + "\n")
		if err == nil {
			err = bw.Flush()
		}
		return err
	}
	if result.Action != "cancel" && result.Action != "decline" {
		return errString("unexpected action " + result.Action)
	}
	body, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "Computer Use was not approved"}},
		"isError": true,
	})
	done, _ := json.Marshal(mcpMessage{JSONRPC: "2.0", ID: call.ID, Result: body})
	_, err = bw.WriteString(string(done) + "\n")
	if err == nil {
		err = bw.Flush()
	}
	return err
}

type errString string

func (e errString) Error() string { return string(e) }

// endingStdin is the stdin of an lcu that has just ended: a write breaks the pipe, and the process's stdout closes with
// it, so its exit is seen only after the write failed.
type endingStdin struct{ stdout *io.PipeWriter }

func (e endingStdin) Write([]byte) (int, error) {
	_ = e.stdout.Close()
	return 0, io.ErrClosedPipe
}

func (endingStdin) Close() error { return nil }

// A write that fails because lcu has just ended fails the call as the process's end, the same error as when the exit
// is seen first, not as a broken pipe.
func TestLCUWriteToAnEndedProcessSaysItEnded(t *testing.T) {
	sess := newLCUSession(nil)
	stdout, ended := io.Pipe()
	sess.attachPipes(stdout, endingStdin{ended})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := sess.call(ctx, "js", map[string]any{"code": "return 1"}, "", ""); !errors.Is(err, errLCUEnded) {
		t.Fatalf("err = %v, want the process ended", err)
	}
}

// erringStdin is a stdin whose writes fail with err.
type erringStdin struct{ err error }

func (e erringStdin) Write([]byte) (int, error) { return 0, e.err }

func (erringStdin) Close() error { return nil }

// A write that fails for another reason than a closed pipe is that error, not the process's end.
func TestLCUWriteErrorIsItselfUnlessThePipeClosed(t *testing.T) {
	sess := newLCUSession(nil)
	stdout, ended := io.Pipe()
	t.Cleanup(func() { _ = ended.Close() }) // its readLoop ends with the test
	failed := errors.New("no space left on the device")
	sess.attachPipes(stdout, erringStdin{failed})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := sess.call(ctx, "js", map[string]any{"code": "return 1"}, "", "")
	if !errors.Is(err, failed) || errors.Is(err, errLCUEnded) {
		t.Fatalf("err = %v, want the write's own error", err)
	}
}
