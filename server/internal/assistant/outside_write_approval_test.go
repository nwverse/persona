package assistant

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func outsideWriteFingerprint() map[string]any {
	return map[string]any{
		"version":        1,
		"audience":       "agent-api:test-instance",
		"method":         http.MethodPost,
		"path":           "/agent/v1/conversations/123/messages",
		"query":          "clientId=abc",
		"body_sha256":    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"account":        "1",
		"expires_at":     time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano),
		"approve_option": "Approve",
	}
}

func outsideWriteRequest(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(outsideFixture())
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	write := outsideWriteFingerprint()
	in["recommend"] = "Deny"
	in["ref"] = ""
	in["options"] = []map[string]any{
		{"label": "Deny", "leads_to": "Nothing is sent"},
		{"label": "Approve", "leads_to": "The exact request runs once"},
	}
	in["reversible"] = false
	in["allow_default"] = false
	in["write"] = write
	return in, write
}

func consumeOutsideWrite(t *testing.T, e *todoEnv, id string, write map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(write)
	w := e.doAs("decision", http.MethodPost, "/api/v1/decisions/"+id+"/consume", string(raw))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func raiseOutsideWrite(t *testing.T, e *todoEnv) (string, string, map[string]any) {
	t.Helper()
	in, write := outsideWriteRequest(t)
	code, out := outsideCall(t, e, "agentapi", http.MethodPost, "/api/v1/decisions", in)
	if code != http.StatusOK || out.Decision == nil {
		t.Fatalf("raise write decision: %d %+v", code, out)
	}
	return out.Decision.ID, out.Decision.AskTodoID, write
}

func TestOutsideDecisionOwnerLookupUsesTheDurableBook(t *testing.T) {
	e := newTodoEnv(t, nil)
	id, _, _ := raiseOutsideWrite(t, e)
	helloed := make(chan struct{})
	close(helloed)
	bus := &BusHub{svc: e.s, helloed: helloed}
	for _, tc := range []struct {
		id   string
		want bool
	}{{id, true}, {"odc_missing", false}} {
		raw, _ := json.Marshal(map[string]string{"id": tc.id})
		answer, err := bus.Answer(t.Context(), assistantwire.MethodDecisionOwner, raw)
		var out map[string]bool
		_ = json.Unmarshal(answer, &out)
		if err != nil || out["found"] != tc.want {
			t.Fatalf("owner lookup %s: %s, err=%v", tc.id, answer, err)
		}
	}
}

func TestOutsideWriteApprovalSupportsOptions(t *testing.T) {
	e := newTodoEnv(t, nil)
	in, write := outsideWriteRequest(t)
	write["method"] = http.MethodOptions
	in["write"] = write
	code, out := outsideCall(t, e, "agentapi", http.MethodPost, "/api/v1/decisions", in)
	if code != http.StatusOK || out.Decision == nil {
		t.Fatalf("raise OPTIONS write: %d %+v", code, out)
	}
	if rec := answerAsk(e.s, out.Decision.AskTodoID, map[string]string{"option": "Approve"}); rec.Code != http.StatusOK {
		t.Fatalf("approve OPTIONS: %d %s", rec.Code, rec.Body)
	}
	if code, result := consumeOutsideWrite(t, e, out.Decision.ID, write); code != http.StatusOK || result["ok"] != true {
		t.Fatalf("consume OPTIONS: %d %+v", code, result)
	}
}

func TestOutsideWriteApprovalCardUsesTheVerifiedRequestNotCallerText(t *testing.T) {
	e := newTodoEnv(t, nil)
	in, _ := outsideWriteRequest(t)
	in["title"] = "Read messages"
	in["background"] = "This harmless read only lists messages."
	in["options"] = []map[string]any{
		{"label": "Deny", "leads_to": "Nothing is read"},
		{"label": "Approve", "leads_to": "Only read the messages"},
	}
	code, out := outsideCall(t, e, "agentapi", http.MethodPost, "/api/v1/decisions", in)
	if code != http.StatusOK || out.Decision == nil {
		t.Fatalf("raise: %d %+v", code, out)
	}
	got := out.Decision
	if got.Title != "Agent API write: POST /agent/v1/conversations/123/messages" {
		t.Fatalf("caller disguised the write title: %q", got.Title)
	}
	if !strings.Contains(got.Background, "POST /agent/v1/conversations/123/messages?clientId=abc") ||
		strings.Contains(got.Background, "harmless read") {
		t.Fatalf("card background does not describe the exact request: %q", got.Background)
	}
	if len(got.Options) != 2 || got.Options[0].Label != "Deny" || got.Options[1].Label != "Approve" ||
		got.Options[1].LeadsTo != "Run this exact request once" {
		t.Fatalf("caller controlled the write options: %+v", got.Options)
	}
	if got.Recommend != "Deny" || got.Reversible || got.WhyOwner != "visible_to_others" {
		t.Fatalf("unsafe caller-controlled write semantics: %+v", got)
	}
}

func TestOutsideWriteApprovalConsumesOnlyTheApprovedRequestOnce(t *testing.T) {
	e := newTodoEnv(t, nil)
	id, askID, write := raiseOutsideWrite(t, e)
	if code, _ := consumeOutsideWrite(t, e, id, write); code != http.StatusConflict {
		t.Fatalf("pending decision: %d, want 409", code)
	}
	if rec := answerAsk(e.s, askID, map[string]string{"option": "Deny"}); rec.Code != http.StatusOK {
		t.Fatalf("deny decision: %d %s", rec.Code, rec.Body)
	}
	if code, _ := consumeOutsideWrite(t, e, id, write); code != http.StatusForbidden {
		t.Fatalf("denied decision: %d, want 403", code)
	}

	id, askID, write = raiseOutsideWrite(t, e)
	if rec := answerAsk(e.s, askID, map[string]string{"option": "Approve"}); rec.Code != http.StatusOK {
		t.Fatalf("approve decision: %d %s", rec.Code, rec.Body)
	}
	if code, out := consumeOutsideWrite(t, e, id, write); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("approved request: %d %+v", code, out)
	}
	if code, _ := consumeOutsideWrite(t, e, id, write); code != http.StatusConflict {
		t.Fatalf("replayed approval: %d, want 409", code)
	}
}

func TestOutsideWriteApprovalRejectsChangedScope(t *testing.T) {
	e := newTodoEnv(t, nil)
	id, askID, write := raiseOutsideWrite(t, e)
	if rec := answerAsk(e.s, askID, map[string]string{"option": "Approve"}); rec.Code != http.StatusOK {
		t.Fatalf("approve decision: %d %s", rec.Code, rec.Body)
	}
	changes := []map[string]any{
		{"body_sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		{"query": "clientId=changed"},
		{"account": "2"},
		{"audience": "agent-api:other-instance"},
		{"method": http.MethodPut},
		{"path": "/agent/v1/conversations/999/messages"},
		{"expires_at": time.Now().Add(11 * time.Minute).UTC().Format(time.RFC3339Nano)},
	}
	for _, change := range changes {
		changed := outsideWriteFingerprint()
		for key, value := range change {
			changed[key] = value
		}
		if code, _ := consumeOutsideWrite(t, e, id, changed); code != http.StatusForbidden {
			t.Fatalf("changed scope %v: %d, want 403", change, code)
		}
	}
	if code, out := consumeOutsideWrite(t, e, id, write); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("a mismatch consumed the valid approval: %d %+v", code, out)
	}
}

func TestOutsideWriteApprovalRejectsExpiryDefaultAndConcurrentReplay(t *testing.T) {
	e := newTodoEnv(t, nil)
	in, _ := outsideWriteRequest(t)
	in["allow_default"] = true
	code, out := outsideCall(t, e, "agentapi", http.MethodPost, "/api/v1/decisions", in)
	if code != http.StatusBadRequest {
		t.Fatalf("a write approval with a default answer: %d %+v", code, out)
	}

	write := outsideWriteFingerprint()
	expires := time.Now().Add(200 * time.Millisecond)
	write["expires_at"] = expires.UTC().Format(time.RFC3339Nano)
	// Raise a fresh decision whose persisted expiry is the short one.
	in, _ = outsideWriteRequest(t)
	in["write"] = write
	code, out = outsideCall(t, e, "agentapi", http.MethodPost, "/api/v1/decisions", in)
	if code != http.StatusOK || out.Decision == nil {
		t.Fatalf("short-lived decision: %d %+v", code, out)
	}
	id, askID := out.Decision.ID, out.Decision.AskTodoID
	if rec := answerAsk(e.s, askID, map[string]string{"option": "Approve"}); rec.Code != http.StatusOK {
		t.Fatalf("approve expiring decision: %d %s", rec.Code, rec.Body)
	}
	if wait := time.Until(expires) + 5*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}
	if code, _ := consumeOutsideWrite(t, e, id, write); code != http.StatusForbidden {
		t.Fatalf("expired approval: %d, want 403", code)
	}

	id, askID, write = raiseOutsideWrite(t, e)
	if rec := answerAsk(e.s, askID, map[string]string{"option": "Approve"}); rec.Code != http.StatusOK {
		t.Fatalf("approve concurrent decision: %d %s", rec.Code, rec.Body)
	}
	codes := make(chan int, 8)
	for range 8 {
		go func() {
			code, _ := consumeOutsideWrite(t, e, id, write)
			codes <- code
		}()
	}
	ok, conflicts := 0, 0
	for range 8 {
		switch <-codes {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflicts++
		}
	}
	if ok != 1 || conflicts != 7 {
		t.Fatalf("concurrent consume: %d succeeded, %d conflicted", ok, conflicts)
	}
}

func TestOutsideWriteApprovalReplaySurvivesManagerRestart(t *testing.T) {
	st := &memStore{}
	e := newTodoEnv(t, st)
	id, askID, write := raiseOutsideWrite(t, e)
	if rec := answerAsk(e.s, askID, map[string]string{"option": "Approve"}); rec.Code != http.StatusOK {
		t.Fatalf("approve decision: %d %s", rec.Code, rec.Body)
	}
	if code, _ := consumeOutsideWrite(t, e, id, write); code != http.StatusOK {
		t.Fatalf("first consume: %d", code)
	}
	restarted := newServiceOn(t, e.f, st)
	e = &todoEnv{t: t, s: restarted, f: e.f}
	if code, _ := consumeOutsideWrite(t, e, id, write); code != http.StatusConflict {
		t.Fatalf("replay after restart: %d, want 409", code)
	}
}
