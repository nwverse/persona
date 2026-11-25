package extmgr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/extwire"
)

func actionFingerprint(at time.Time) assistantwire.OutsideWriteApproval {
	sum := sha256.Sum256(nil)
	return assistantwire.OutsideWriteApproval{
		Version: 1, Audience: "agent-api:instance", Method: http.MethodPost,
		Path: "/agent/v1/conversations/feed/messages", BodySHA256: hex.EncodeToString(sum[:]),
		ExpiresAt: at, ApproveOption: "Approve",
	}
}

func TestActionApprovalBookConsumesOnceAndChecksExpiry(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	book := actionApprovalBook{now: func() time.Time { return now }}
	id := connector.ActionApprovalID("action:0123456789abcdef0123456789abcdef")
	if err := book.register(id, now.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	in := actionFingerprint(now.Add(10 * time.Minute))
	if _, err := book.consume(id, in); err != nil {
		t.Fatal(err)
	}
	if _, err := book.consume(id, in); err == nil || err.Error() != "the action approval was already consumed" {
		t.Fatalf("replay: %v", err)
	}
	if _, err := book.consume(connector.ActionApprovalID("action:forged"), in); err == nil {
		t.Fatal("an unregistered approval was consumed")
	}

	extended := connector.ActionApprovalID("action:extended")
	if err := book.register(extended, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := book.consume(extended, actionFingerprint(now.Add(10*time.Minute))); err == nil {
		t.Fatal("the caller extended the registered expiry")
	}
	if _, err := book.consume(extended, actionFingerprint(now.Add(4*time.Minute))); err != nil {
		t.Fatalf("the unextended request remains usable: %v", err)
	}

	expired := connector.ActionApprovalID("action:expired")
	if err := book.register(expired, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := book.consume(expired, actionFingerprint(now.Add(time.Minute))); err == nil {
		t.Fatal("an expired action approval was consumed")
	}
}

func TestActionApprovalHTTPRequiresDecisionRole(t *testing.T) {
	h := &Manager{}
	mux := http.NewServeMux()
	h.actionApprovalRoutes(mux)
	register := func(seed string) string {
		id := connector.ActionApprovalID(seed)
		if err := h.registerActionApproval(t.Context(), id, time.Now().Add(15*time.Minute)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	call := func(id string, role string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(actionFingerprint(time.Now().Add(10 * time.Minute)))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/connector-decisions/"+id+"/consume", bytes.NewReader(body))
		req.Header.Set(extwire.HeaderRole, role)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res
	}
	if res := call(register("action:denied-role"), "full"); res.Code != http.StatusForbidden {
		t.Fatalf("full role consume = %d, %s", res.Code, res.Body.String())
	}
	id := register("action:http-success")
	if res := call(id, "decision"); res.Code != http.StatusOK {
		t.Fatalf("decision consume = %d, %s", res.Code, res.Body.String())
	}
	if res := call(id, "decision"); res.Code != http.StatusConflict {
		t.Fatalf("replay = %d, %s", res.Code, res.Body.String())
	}
	var failure *actionApprovalFailure
	if err := h.registerActionApproval(t.Context(), "bad", time.Now().Add(time.Minute)); !errors.As(err, &failure) {
		t.Fatalf("invalid registration: %v", err)
	}
}

func TestActionApprovalFingerprintUsesTheOutsideWriteContract(t *testing.T) {
	book := actionApprovalBook{}
	for name, change := range map[string]func(*assistantwire.OutsideWriteApproval){
		"method":  func(in *assistantwire.OutsideWriteApproval) { in.Method = "BREW" },
		"query":   func(in *assistantwire.OutsideWriteApproval) { in.Query = "ok=1\r\ninjected" },
		"account": func(in *assistantwire.OutsideWriteApproval) { in.Account = " 2" },
		"hash": func(in *assistantwire.OutsideWriteApproval) {
			in.BodySHA256 = strings.ToUpper(in.BodySHA256)
		},
	} {
		t.Run(name, func(t *testing.T) {
			id := connector.ActionApprovalID("action:invalid-" + name)
			if err := book.register(id, time.Now().Add(5*time.Minute)); err != nil {
				t.Fatal(err)
			}
			in := actionFingerprint(time.Now().Add(time.Minute))
			change(&in)
			if _, err := book.consume(id, in); err == nil {
				t.Fatal("an invalid fingerprint consumed the approval")
			}
		})
	}
}

// approvalReplyConnector spends the approval its Reply receives through the
// same context contract as the real plugin.
type approvalReplyConnector struct{ inboxConnector }

func (c *approvalReplyConnector) Reply(ctx context.Context, r connector.InboxReply) error {
	if err := connector.RegisterActionApproval(ctx, connector.ActionApprovalID("reply:"+r.ClientID), time.Now().Add(10*time.Minute)); err != nil {
		return err
	}
	return c.inboxConnector.Reply(ctx, r)
}

func TestInboxReplyInstallsTheActionApprovalRegistrar(t *testing.T) {
	h := newTestManager(t)
	expires := time.Now().Add(10 * time.Minute).UTC()
	h.connectors = h.newConnectors(nil)
	if err := h.addConnector(&approvalReplyConnector{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"source": "example", "reply": connector.InboxReply{Thread: "chat:C1", Text: "approved", ClientID: "reply-1"}})
	_, handled, err := h.AppCall(t.Context(), assistantwire.AppCall{AppID: assistantwire.AppInbox, Method: assistantwire.InboxReply, Params: raw})
	if !handled || err != nil {
		t.Fatalf("reply: handled=%v err=%v", handled, err)
	}
	id := connector.ActionApprovalID("reply:reply-1")
	if _, err := h.actionApprovals.consume(id, actionFingerprint(expires)); err != nil {
		t.Fatalf("the reply did not register its approval: %v", err)
	}
}

func TestNodeActionApprovalBindsTheAgentAudience(t *testing.T) {
	h := newTestManager(t)
	h.opts.Devices = &mirrorDevices{ids: []string{"box"}, names: map[string]string{"box": "Box"}}
	id := connector.ActionApprovalID("action:node-audience")
	expires := time.Now().Add(10 * time.Minute).UTC()
	audience := "agent-api:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	raw, _ := json.Marshal(map[string]any{"id": id, "expires_at": expires, "audience": audience})
	if _, err := h.RecordNodeActionApproval(t.Context(), "box", raw); err != nil {
		t.Fatal(err)
	}
	changed := actionFingerprint(expires)
	changed.Audience = "agent-api:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := h.actionApprovals.consume(id, changed); err == nil {
		t.Fatal("a node capability was consumed for a different Agent credential")
	}
	matching := actionFingerprint(expires)
	matching.Audience = audience
	if _, err := h.actionApprovals.consume(id, matching); err != nil {
		t.Fatalf("the matching audience could not consume: %v", err)
	}
}
