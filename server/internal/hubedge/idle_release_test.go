//go:build unix

package hubedge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// envTestOut is where the turnsBusy slot writes what happened to the switch it asked for.
const envTestOut = "HUBEDGE_TEST_OUT"

// turnsBusySlot is the hub of the race in #10826 (model counterexample HI-CE1): it asks for an update switch and says it
// wants to be released only while idle, then a conversation starts working right after its idle check. It speaks the
// edge's wire protocol itself, so this test runs against an edge of any age: an edge that releases it anyway writes
// "released while busy" to envTestOut; one that asks first is told it is busy.
func turnsBusySlot(name string) int {
	in, out, err := slotPipes()
	if err != nil {
		return 2
	}
	w := &lineWriter{w: out}
	// Read raw lines, not Msg: an edge's fields this build does not know (ask_idle's seq) are echoed as they came.
	msgs := make(chan map[string]any, 8)
	go func() {
		defer close(msgs)
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				msgs <- m
			}
		}
	}()
	_ = w.send(Msg{T: MsgHello, Proto: Proto, Version: name, Build: 1, PID: os.Getpid(), Slot: os.Getenv(EnvSlot)})
	var path string
	for _, kv := range strings.Split(os.Getenv(EnvSockets), ";") {
		if p, sock, ok := strings.Cut(kv, "="); ok && p == os.Getenv(envTestPort) {
			path = sock
		}
	}
	tcp, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:"+os.Getenv(envTestPort))
	ln, err := ListenSlot(path, tcp)
	if err != nil {
		_ = w.send(Msg{T: MsgFailed, Reason: err.Error()})
		return 1
	}
	busy := make(chan bool, 1)
	busy <- false
	result := func(s string) { _ = os.WriteFile(os.Getenv(envTestOut), []byte(s), 0o600) }
	srv := &http.Server{Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/switch" {
			// The update's own request, as Slot.Switch sends it for KindUpdate: idle is the opt-in to be asked.
			b, _ := json.Marshal(map[string]any{"t": MsgSwitch, "binary": r.URL.Query().Get("to"), "kind": KindUpdate,
				"expect": 2, "idle": true})
			<-busy
			busy <- true // the turn that starts after the hub's idle check
			w.mu.Lock()
			_, _ = w.w.Write(append(b, '\n'))
			w.mu.Unlock()
		}
		fmt.Fprintf(rw, "%s %s", name, os.Getenv(EnvSlot))
	})}
	go func() { _ = srv.Serve(ln) }()
	_ = w.send(Msg{T: MsgReady})
	for m := range msgs {
		reason, _ := m["reason"].(string)
		switch m["t"] {
		case "ask_idle":
			b := <-busy
			busy <- b
			reply := map[string]any{"t": "idle", "ok": !b, "seq": m["seq"]}
			if b {
				reply["reason"] = "1 conversation is working"
			}
			line, _ := json.Marshal(reply)
			w.mu.Lock()
			_, _ = w.w.Write(append(line, '\n'))
			w.mu.Unlock()
		case MsgSwitchFailed:
			result("switch failed: " + reason)
		case MsgRelease:
			if b := <-busy; b {
				result("released while busy")
			} else {
				result("released")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			return 0
		}
	}
	return 0
}

// An update switch is asked for by a hub that was idle; a conversation starts working before the new slot is ready.
// The edge asks the hub once more right before it releases it: busy, the new slot goes and the hub goes on serving.
func TestUpdateSwitchDoesNotReleaseABusySlot(t *testing.T) {
	out := t.TempDir() + "/result"
	t.Setenv(envTestOut, out)
	te := startEdge(t, "turns-busy", nil)
	q := url.Values{"to": {te.binary("v2")}}
	if _, err := te.get("/switch?" + q.Encode()); err != nil {
		t.Fatal(err)
	}
	var got string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(out); err == nil {
			got = string(b)
			break
		}
	}
	switch {
	case got == "":
		t.Fatal("the switch never ended")
	case strings.HasPrefix(got, "released"):
		t.Fatalf("the edge %s the slot a conversation is working on (the update cut its turn off)", got)
	case !strings.Contains(got, "busy") && !strings.Contains(got, "working"):
		t.Fatalf("the switch failed for another reason: %q", got)
	}
	if body := te.waitFor("turns-busy"); !strings.HasPrefix(body, "turns-busy ") {
		t.Fatalf("GET / = %q, want the busy slot still serving", body)
	}
}

// Through the Slot API the hub uses: an update switch from a slot that says it is busy, or does not answer within
// IdleWait, fails with ErrBusy and the slot goes on serving; one that says it is idle hands over. A switch of another
// kind, or from a slot that never set an answer, is not asked (the edge's behaviour before #10826).
func TestUpdateSwitchAsksTheSlotWhetherItIsIdle(t *testing.T) {
	te := startEdge(t, "v1", func(o *Options) { o.IdleWait = 500 * time.Millisecond })
	for _, idle := range []string{"no", "mute"} {
		q := url.Values{"to": {te.binary("v2")}, "expect": {"2"}, "kind": {KindUpdate}, "idle": {idle}}
		body, err := te.get("/switch?" + q.Encode())
		if err != nil || !strings.Contains(body, "(busy true)") {
			t.Fatalf("idle=%s: switch answered %q %v, want ErrBusy", idle, body, err)
		}
		te.waitFor("v1")
	}
	q := url.Values{"to": {te.binary("v2")}, "expect": {"2"}, "kind": {KindUpdate}, "idle": {"yes"}}
	go func() { _, _ = te.get("/switch?" + q.Encode()) }() // answered by v1 as it is released, or cut off
	te.waitFor("v2")
}
