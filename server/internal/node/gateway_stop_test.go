package node

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestGatewayStoppedProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       proto.StopCause
	}{
		{"quota", "Request rejected (429) · upstream account quota usage limit has been reached; an account becomes available again at 18:02 UTC (in 12h7m)", proto.StopUsageLimit},
		{"capacity", "Request rejected (429) · upstream account capacity is temporarily unavailable", proto.StopTransientError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, home := herdrNode(t)
			provenanceHerdr(n)
			cwd := mkdir(t, home+"/project")
			at := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
			writeTranscript(t, home, cwd, sidA, at,
				`{"type":"user","uuid":"u1","timestamp":"`+at.Format(time.RFC3339)+`","cwd":"`+cwd+`","message":{"role":"user","content":"continue"}}`,
				`{"type":"assistant","uuid":"a1","timestamp":"`+at.Format(time.RFC3339)+`","message":{"role":"assistant","model":"example-model","content":[{"type":"text","text":"`+strings.Repeat("Working. ", 300)+`"}]}}`,
				`{"type":"assistant","uuid":"e1","timestamp":"`+at.Format(time.RFC3339)+`","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API Error: `+tc.text+`"}]}}`)
			n.state.put(sidA, sessionState{Launcher: "claude", Cwd: cwd, AppMode: "plan"})
			offlineCache.Lock()
			offlineCache.at = time.Time{}
			offlineCache.Unlock()
			row := byID(listSessions(t, n))[sidA]
			if row.Capabilities == nil || row.Capabilities.StopCause != tc.want || row.Capabilities.Send || row.ReadOnlyReason == "" {
				t.Fatalf("stopped row: status=%q capabilities=%+v reason=%q", row.Status, row.Capabilities, row.ReadOnlyReason)
			}
			if err := n.supervisorResumeAllowed(sidA, "", ""); err != nil {
				t.Fatal(err)
			}
			if tc.want == proto.StopUsageLimit && (row.TurnErrorKind != proto.TurnErrorUsageLimit || row.TurnErrorResetsAt == nil) {
				t.Fatalf("quota has no reset: kind=%q reset=%v", row.TurnErrorKind, row.TurnErrorResetsAt)
			}
			if tc.want == proto.StopTransientError && row.TurnErrorKind != proto.TurnErrorTransient {
				t.Fatalf("capacity kind = %q", row.TurnErrorKind)
			}
			// Explicit stops always win, even while the same gateway error remains in history.
			for _, cause := range []proto.StopCause{proto.StopUser, proto.StopExit} {
				n.state.stopped(sidA, cause)
				row.Capabilities = nil
				n.markStopped(&row)
				if n.stopCause(sidA) != cause || n.supervisorResumeAllowed(sidA, "", "") == nil {
					t.Fatalf("gateway error overrode %s", cause)
				}
			}
			// Consuming a previous resume cannot grant a second start from the old failure.
			n.state.stopped(sidA, proto.StopUnknown)
			row.Capabilities = nil
			n.markStopped(&row)
			if n.stopCause(sidA) != proto.StopUnknown || n.supervisorResumeAllowed(sidA, "", "") == nil {
				t.Fatal("old error reauthorized an uncertain start")
			}
			// A reply quoting the error is never recovery provenance.
			if kind, _ := transcript.GatewayErrorKind("I saw: "+tc.text, at); kind != "" {
				t.Fatal("ordinary reply classified as a gateway failure")
			}
		})
	}
}
