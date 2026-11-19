package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// notFoundReason is the reason a read's not_found gives (data.reason), "" for none.
func notFoundReason(err error) string {
	var re *rpc.Error
	if !errors.As(err, &re) || len(re.Data) == 0 {
		return ""
	}
	var data struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(re.Data, &data)
	return data.Reason
}

// A read of a session this machine has no transcript for (an unknown id: a stale link, a client that sent the wrong
// field) answers not_found with the words it always had, which the hub serves as 404, not 502 internal (#9551), and
// says why: the node has no record of it (#11388). The other callers of file keep the plain error: the manager takes a
// not_found send as "the session is gone" and withdraws its card, which a missing file alone must not do.
func TestReadsOfAnUnknownSessionAreNotFound(t *testing.T) {
	home := t.TempDir()
	n := &Node{home: home, paths: map[string]string{}, files: map[string]*transcript.File{}, procs: map[string]*assistantProc{},
		agents: map[string]host.Agent{}, state: &stateFile{Sessions: map[string]sessionState{}}}
	const unknown = "99999999-2222-3333-4444-555555555555"
	const words = "transcript for session " + unknown + " not found"
	for _, method := range []string{"session.items", "session.item", "session.image", "session.visualization"} {
		_, err := n.handle(context.Background(), method, json.RawMessage(`{"session_id":"`+unknown+`","item_id":"x","turns":2}`))
		if rpcCode(err) != codeNotFound || err.Error() != words || notFoundReason(err) != proto.NotFoundUnknownSession {
			t.Errorf("%s: want not_found %q (unknown_session), got code %q reason %q: %v", method, words, rpcCode(err), notFoundReason(err), err)
		}
	}
	if _, err := n.handle(context.Background(), "session.items", json.RawMessage(`{"session_id":"`+unknown+`","user_inputs":true}`)); rpcCode(err) != codeNotFound || notFoundReason(err) != proto.NotFoundUnknownSession {
		t.Errorf("user_inputs: want not_found (unknown_session), got code %q: %v", rpcCode(err), err)
	}
	// A subagent of a session with no transcript is not there either, for its session's reason.
	if _, err := n.handle(context.Background(), "session.items", json.RawMessage(`{"session_id":"`+unknown+subagentSep+`a1"}`)); rpcCode(err) != codeNotFound || notFoundReason(err) != proto.NotFoundUnknownSession {
		t.Errorf("subagent: want not_found (unknown_session), got code %q: %v", rpcCode(err), err)
	}
	if _, err := n.file(unknown); err == nil || rpcCode(err) != "" || err.Error() != words {
		t.Errorf("file: want the plain error %q, got code %q: %v", words, rpcCode(err), err)
	}

	// While the node is still loading its records from the hub, an id missing from them may still be Persona's: no
	// reason is claimed.
	n.state.beginLoad()
	_, err := n.handle(context.Background(), "session.items", json.RawMessage(`{"session_id":"`+unknown+`","turns":2}`))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != codeNotFound || err.Error() != words || len(re.Data) != 0 {
		t.Errorf("loading: want not_found %q without data, got %v", words, err)
	}
	n.state.endLoad()
}

// A session the node knows (here one Persona started an hour ago, and an assistant conversation) whose transcript is
// gone, or that never got a message, is not_found with the reason no_transcript, never an empty page that shows an
// empty chat as if nothing had been said (#11388). The message is the one the node has always given, and the reason
// is all the data: a read delivers nothing, so it says nothing of delivery.
func TestReadsOfAGoneTranscriptAreNotFound(t *testing.T) {
	n, home := testNode(t)
	long := time.Now().Add(-time.Hour).UTC()
	const agent, chat = "11111111-2222-3333-4444-555555555555", "22222222-2222-3333-4444-555555555555"
	n.state.put(agent, sessionState{Launcher: "claude", Cwd: home, StartedAt: long})
	n.state.put(chat, sessionState{Launcher: "claude", Cwd: home + "/.persona/assistant", StartedAt: long})
	for _, id := range []string{agent, chat} {
		words := "transcript for session " + id + " not found"
		for _, call := range []struct{ method, params string }{
			{"session.items", `{"session_id":"` + id + `","turns":2}`},
			{"session.items", `{"session_id":"` + id + `","limit":80}`},
			{"session.items", `{"session_id":"` + id + `","user_inputs":true}`},
			{"session.item", `{"session_id":"` + id + `","item_id":"x"}`},
			{"session.image", `{"session_id":"` + id + `","item_id":"x"}`},
			{"session.visualization", `{"session_id":"` + id + `","item_id":"x"}`},
			{"session.items", `{"session_id":"` + id + subagentSep + `a1","turns":2}`}, // its subagent: its reason
		} {
			res, err := n.handle(context.Background(), call.method, json.RawMessage(call.params))
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != codeNotFound || re.Message != words || string(re.Data) != `{"reason":"no_transcript"}` {
				t.Errorf("%s %s: want not_found %q {reason: no_transcript}, got %v %v", call.method, call.params, words, res, err)
			}
		}
	}
	// Its watch still waits for the transcript, as for a session that has just started (pending_watch_test.go).
	if err := n.watch(agent, 0); err != nil {
		t.Errorf("watch: %v", err)
	}
	n.unwatch(agent)
}
