package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type outboundMemoryStore struct {
	*inboxMemStore
	auditMu     sync.Mutex
	audit       []db.SentAsUser
	failAudit   bool
	failOutcome bool
	claims      map[string]string
}

func (st *outboundMemoryStore) ClaimSend(_ context.Context, id, _, _ string) (string, error) {
	st.auditMu.Lock()
	defer st.auditMu.Unlock()
	if st.claims == nil {
		st.claims = map[string]string{}
	}
	before := st.claims[id]
	if before == "" {
		st.claims[id] = "sending"
	}
	return before, nil
}

func (st *outboundMemoryStore) SettleSend(_ context.Context, id, state string) error {
	st.auditMu.Lock()
	defer st.auditMu.Unlock()
	st.claims[id] = state
	return nil
}

func (st *outboundMemoryStore) CompareSetting(ctx context.Context, key string, revision int64, value any) (bool, error) {
	var before StandingAuthorizations
	_, err := st.Setting(ctx, key, &before)
	if err != nil || before.Revision != revision {
		return false, err
	}
	return true, st.PutSetting(ctx, key, value)
}

func (st *outboundMemoryStore) RecordSentAsUser(_ context.Context, row db.SentAsUser) (int64, error) {
	st.auditMu.Lock()
	defer st.auditMu.Unlock()
	if st.failAudit {
		return 0, errors.New("audit unavailable")
	}
	row.ID = int64(len(st.audit) + 1)
	st.audit = append(st.audit, row)
	return row.ID, nil
}

func (st *outboundMemoryStore) SetSentAsUserOutcome(_ context.Context, id int64, outcome string) error {
	st.auditMu.Lock()
	defer st.auditMu.Unlock()
	if st.failOutcome {
		return errors.New("outcome write failed")
	}
	var through map[string]string
	_ = json.Unmarshal(st.audit[id-1].LetThrough, &through)
	through["outcome"] = outcome
	st.audit[id-1].LetThrough, _ = json.Marshal(through)
	return nil
}

type outboundConnector struct {
	extensionLark
	identities []string
	fail       error
}

func (*outboundConnector) ID() string { return "larkcli" }
func (c *outboundConnector) Reply(ctx context.Context, reply connector.InboxReply) error {
	c.identities = append(c.identities, connector.SendIdentity(ctx))
	if c.fail != nil {
		return c.fail
	}
	return c.extensionLark.Reply(ctx, reply)
}

func messageTestGrant(scenario string) StandingAuthorization {
	return StandingAuthorization{ID: "sa_messages", Actions: []string{"send"}, Conditions: []string{}, Quote: "Send updates in the example group.",
		Source: "settings", GrantedAt: time.Now().UTC(), Reversible: true,
		Messages: &MessageAuthorization{Channel: "larkcli", Target: "oc_example_group", Scenario: scenario, Identity: "bot"}}
}

func outboundTestHub(t *testing.T) (*inboxHub, *outboundMemoryStore, *outboundConnector, *InboxItem) {
	t.Helper()
	h, _, it := handledHub(t)
	st := &outboundMemoryStore{inboxMemStore: h.st}
	h.store = func() Store { return st }
	c := &outboundConnector{}
	h.in.conns = []connector.Connector{c}
	it.Source, it.Lark = "larkcli", nil
	it.Connector = &InboxConnector{Thread: "larkcli:chat:oc_example_group:thread:omt_example", CanReply: true}
	return h, st, c, it
}

func putMessageGrant(t *testing.T, st Store, scenario string) {
	t.Helper()
	if err := st.PutSetting(context.Background(), standingAuthorizationsKey, StandingAuthorizations{
		Revision: 1, Authorizations: []StandingAuthorization{messageTestGrant(scenario)},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOutboundScopeIsStructuredAndConservative(t *testing.T) {
	grant := messageTestGrant("inbox_reply")
	if err := checkStandingGrant(grant); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*StandingAuthorization){
		func(g *StandingAuthorization) { g.Messages.Channel = "other" },
		func(g *StandingAuthorization) { g.Messages.Target = "oc_example_*" },
		func(g *StandingAuthorization) { g.Messages.Target = "oc_example_group:thread:other" },
		func(g *StandingAuthorization) { g.Messages.Scenario = "anything" },
		func(g *StandingAuthorization) { g.Messages.Identity = "auto" },
		func(g *StandingAuthorization) { g.Actions = []string{"send", "delete"} },
		func(g *StandingAuthorization) { g.Conditions = []string{"safe messages only"} },
		func(g *StandingAuthorization) { g.MachineID = "computer" },
		func(g *StandingAuthorization) { g.Directory = "/code/example" },
		func(g *StandingAuthorization) { g.Reversible = false },
	} {
		g := messageTestGrant("inbox_reply")
		mutate(&g)
		if checkStandingGrant(g) == nil {
			t.Fatalf("unsafe grant accepted: %+v / %+v", g, g.Messages)
		}
	}
}

func TestOutboundInboxAutomaticSendAndRevocation(t *testing.T) {
	ctx := context.Background()
	h, st, c, it := outboundTestHub(t)
	putMessageGrant(t, st, "inbox_reply")
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"The example update is ready."}`); err != nil {
		t.Fatal(err)
	}
	if len(c.got) != 1 || c.got[0].Text != "The example update is ready." || c.identities[0] != "bot" {
		t.Fatalf("approved suggestion did not send as bot: %+v %v", c.got, c.identities)
	}
	if len(st.audit) != 1 || !strings.Contains(string(st.audit[0].LetThrough), `"authorization_id":"sa_messages"`) ||
		!strings.Contains(string(st.audit[0].LetThrough), `"outcome":"sent"`) || !strings.Contains(string(st.audit[0].Target), "oc_example_group") {
		t.Fatalf("missing exact audit: %+v", st.audit)
	}
	if w := serve(h.Service, "DELETE", "/api/v1/assistant/standing-authorizations/sa_messages", ""); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	h.inbox.mu.Lock()
	it.Agent.State = AgentRunning
	h.inbox.mu.Unlock()
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"A later update must wait."}`); err != nil {
		t.Fatal(err)
	}
	if len(c.got) != 1 || currentSuggestion(it) == nil {
		t.Fatal("revoked grant still sent, or manual draft disappeared")
	}
	if grants, err := h.standingForDirectory(ctx, "", "/code/example"); err != nil || len(grants) != 0 {
		t.Fatalf("message scope leaked into repository grants: %+v %v", grants, err)
	}
}

func TestOutboundInboxDefaultsAndScopeMismatchesStayManual(t *testing.T) {
	for _, tc := range []string{"default", "wrong chat", "wrong scenario", "changed identity", "handback", "document changes", "repository send"} {
		t.Run(tc, func(t *testing.T) {
			h, st, c, it := outboundTestHub(t)
			g := messageTestGrant("inbox_reply")
			body := `{"kind":"reply","text":"An example reply."}`
			switch tc {
			case "wrong chat":
				g.Messages.Target = "oc_different_group"
			case "wrong scenario":
				g.Messages.Scenario = "assistant_message"
			case "changed identity":
				_ = st.PutSetting(context.Background(), outboundIdentityKey, "user")
			case "handback":
				body = `{"kind":"reply","text":"Please decide.","handback":true}`
			case "document changes":
				// A document/mutating suggestion is not an automatic plain-text chat reply.
				it.Source = "documents"
			case "repository send":
				g = standingRegistryGrant()
				g.Actions, g.Conditions = []string{"send"}, []string{}
			}
			if tc != "default" {
				_ = st.PutSetting(context.Background(), standingAuthorizationsKey, StandingAuthorizations{Revision: 1, Authorizations: []StandingAuthorization{g}})
			}
			if _, err := suggestTool(t, h, body); err != nil {
				t.Fatal(err)
			}
			if len(c.got) != 0 || currentSuggestion(it) == nil {
				t.Fatalf("%s bypassed confirmation", tc)
			}
		})
	}
}

func TestOutboundAutomaticSendFailsClosedWithoutAudit(t *testing.T) {
	h, st, c, _ := outboundTestHub(t)
	putMessageGrant(t, st, "inbox_reply")
	st.failAudit = true
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Do not send without a ledger."}`); err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("audit failure was hidden: %v", err)
	}
	if len(c.got) != 0 {
		t.Fatal("send escaped without durable audit")
	}
}

func TestOutboundAssistantToolUsesGrantAndSendOnce(t *testing.T) {
	h, st, c, _ := outboundTestHub(t)
	putMessageGrant(t, st, "assistant_message")
	args := json.RawMessage(`{"channel":"larkcli","target":"oc_example_group","text":"Assistant update.","idempotency_key":"example-update-1"}`)
	for attempt := 0; attempt < 2; attempt++ {
		_, err := h.runTool(context.Background(), "server", "s1", "message_send", args)
		if (err != nil) != (attempt > 0) {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if len(c.got) != 1 || len(c.identities) != 1 || c.identities[0] != "bot" {
		t.Fatalf("send-once/identity: %+v %v", c.got, c.identities)
	}
}

func TestOutboundFailureNeverRetriesOrChangesIdentity(t *testing.T) {
	h, st, c, _ := outboundTestHub(t)
	putMessageGrant(t, st, "assistant_message")
	c.fail = errors.New("the bot is not in this chat")
	args := json.RawMessage(`{"channel":"larkcli","target":"oc_example_group","text":"Do not retry automatically.","idempotency_key":"failed-one"}`)
	for attempt := 0; attempt < 2; attempt++ {
		_, err := h.runTool(context.Background(), "server", "s1", "message_send", args)
		if err == nil {
			t.Fatal("failed send reported success")
		}
	}
	if len(c.identities) != 1 || c.identities[0] != "bot" {
		t.Fatalf("retried or changed sender: %v", c.identities)
	}
	if len(st.audit) != 1 || !strings.Contains(string(st.audit[0].LetThrough), "failed_or_unknown") {
		t.Fatalf("failed attempt missing: %+v", st.audit)
	}
}

func TestOutboundInboxRewordingCannotRetryAnUncertainSend(t *testing.T) {
	h, st, c, it := outboundTestHub(t)
	putMessageGrant(t, st, "inbox_reply")
	c.fail = errors.New("delivery outcome unknown")
	for _, text := range []string{"First wording.", "A reworded retry."} {
		h.inbox.mu.Lock()
		it.Agent.State = AgentRunning
		h.inbox.mu.Unlock()
		body, _ := json.Marshal(map[string]string{"kind": "reply", "text": text})
		if _, err := suggestTool(t, h, string(body)); err == nil {
			t.Fatal("uncertain/repeated source-message reply reported success")
		}
	}
	if len(c.identities) != 1 || len(st.audit) != 1 {
		t.Fatalf("rewording caused another attempt: identities=%v audit=%+v", c.identities, st.audit)
	}
}

func TestOutboundPendingAuditNeverTriggersAnotherSend(t *testing.T) {
	h, st, c, _ := outboundTestHub(t)
	putMessageGrant(t, st, "assistant_message")
	st.failOutcome = true
	args := json.RawMessage(`{"channel":"larkcli","target":"oc_example_group","text":"Possibly delivered.","idempotency_key":"pending-one"}`)
	for range 2 {
		if _, err := h.runTool(context.Background(), "server", "s1", "message_send", args); err == nil {
			t.Fatal("pending outcome reported as confirmed delivery")
		}
	}
	if len(c.got) != 1 || len(st.audit) != 1 || !strings.Contains(string(st.audit[0].LetThrough), `"outcome":"pending"`) {
		t.Fatalf("pending audit retried or lost: %+v %+v", c.got, st.audit)
	}
}

func TestOutboundOldTransportIsRejectedBeforeSending(t *testing.T) {
	h, st, _, _ := outboundTestHub(t)
	putMessageGrant(t, st, "assistant_message")
	h.f.inbox = nil
	h.f.answerApp(AppInbox, assistantwire.InboxMessagePolicy, func(json.RawMessage) (any, error) {
		return map[string]bool{}, nil
	})
	sends := 0
	h.f.answerApp(AppInbox, assistantwire.InboxReply, func(json.RawMessage) (any, error) { sends++; return assistantwire.InboxSent{}, nil })
	_, err := h.runTool(context.Background(), "server", "s1", "message_send",
		json.RawMessage(`{"channel":"larkcli","target":"oc_example_group","text":"Must not escape to an old transport.","idempotency_key":"old-one"}`))
	if err == nil || sends != 0 || len(st.audit) != 0 {
		t.Fatalf("old transport was used: sends=%d err=%v", sends, err)
	}
}

func TestOutboundSettingsRequireAFullOwnerDevice(t *testing.T) {
	h, _, _, _ := outboundTestHub(t)
	for _, role := range []string{"answer", "extension"} {
		for _, method := range []string{"GET", "POST"} {
			request := httptest.NewRequest(method, "/api/v1/assistant/outbound-messages", nil)
			request.Header.Set(RoleHeader, role)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != 403 {
				t.Fatalf("%s %s: %d %s", role, method, response.Code, response.Body)
			}
		}
	}
}

func TestOutboundOwnerApprovalCardShowsScopeAndDoesNotSelfApprove(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(fmt.Sprint(owner), func(t *testing.T) {
			h, st, _, _ := outboundTestHub(t)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- serve(h.Service, "POST", "/api/v1/assistant/outbound-messages",
					`{"revision":0,"scope":{"channel":"larkcli","target":"oc_example_group","scenario":"inbox_reply","identity":"bot"}}`)
			}()
			card, ok := awaitCard(h.Service)
			if !ok || card.Kind != proto.IxPermission || !strings.Contains(card.Summary, "oc_example_group") ||
				!strings.Contains(card.Summary, "larkcli") || !strings.Contains(card.Summary, "bot") && !strings.Contains(card.Summary, "机器人") {
				t.Fatalf("missing scope card: %+v", card)
			}
			before, _ := h.standingAuthorizations(context.Background())
			if len(before.Authorizations) != 0 {
				t.Fatal("active before owner approval")
			}
			yes := true
			answer := proto.Resolution{Allow: &yes}
			if !owner {
				answer.AnsweredBy = proto.AnsweredByAssistant
			}
			if _, err := h.Action(context.Background(), Action{Op: "answer", InteractionID: card.ID, Answer: standingJSON(t, answer)}); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-done:
				if response.Code != 200 {
					t.Fatalf("approval response: %d %s", response.Code, response.Body)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("approval did not finish")
			}
			var registry StandingAuthorizations
			_, _ = st.Setting(context.Background(), standingAuthorizationsKey, &registry)
			if (len(registry.Authorizations) == 1) != owner {
				t.Fatalf("owner=%v registry=%+v", owner, registry)
			}
		})
	}
}

func TestOutboundManualMessageNeedsItsOwnPermission(t *testing.T) {
	h, st, c, _ := outboundTestHub(t)
	_ = st.PutSetting(context.Background(), outboundIdentityKey, "user")
	done := make(chan error, 1)
	go func() {
		_, err := h.runTool(context.Background(), "server", "s1", "message_send", json.RawMessage(`{"channel":"larkcli","target":"oc_example_group","text":"Owner-approved example.","idempotency_key":"manual-one"}`))
		done <- err
	}()
	card, ok := awaitCard(h.Service)
	if !ok || card.ToolName != "message_send" || !strings.Contains(card.Summary, "oc_example_group") || !strings.Contains(card.Summary, "Owner-approved example.") {
		t.Fatalf("missing precise manual card: %+v", card)
	}
	if len(c.got) != 0 {
		t.Fatal("sent before owner approval")
	}
	yes := true
	_, err := h.Action(context.Background(), Action{Op: "answer", InteractionID: card.ID, Answer: standingJSON(t, proto.Resolution{Allow: &yes})})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("manual send did not finish")
	}
	if len(c.got) != 1 || c.identities[0] != "user" {
		t.Fatalf("selected sender ignored: %+v %v", c.got, c.identities)
	}
}

func TestOutboundRegistryAndLedgerPersistInDatabase(t *testing.T) {
	ctx := context.Background()
	database := taskRecoveryDatabase(t)
	f := newFakeHub().withMachine()
	f.answerApp(AppInbox, assistantwire.InboxMessagePolicy, func(json.RawMessage) (any, error) { return map[string]bool{"sender_identity": true}, nil })
	sends := 0
	f.answerApp(AppInbox, assistantwire.InboxReply, func(raw json.RawMessage) (any, error) {
		if !strings.Contains(string(raw), `"sender_identity":"bot"`) {
			t.Errorf("sender override missing: %s", raw)
		}
		sends++
		return assistantwire.InboxSent{}, nil
	})
	s := newServiceOn(t, f, database)
	putMessageGrant(t, s.store(), "assistant_message")
	text := "A durable synthetic example."
	payload := standingJSON(t, map[string]string{
		"channel": "larkcli", "target": "oc_example_group", "text": text, "idempotency_key": "durable-one",
	})
	if _, err := s.toolMessageSend(ctx, "server", "s1", payload); err != nil {
		t.Fatal(err)
	}
	rows, _, err := database.SentAsUsers(ctx, 0, 10, "larkcli")
	if err != nil || sends != 1 || len(rows) != 1 || rows[0].Text != text || !strings.Contains(string(rows[0].LetThrough), "sa_messages") ||
		!strings.Contains(string(rows[0].LetThrough), `"outcome": "sent"`) {
		t.Fatalf("durable ledger: %+v sends=%d err=%v", rows, sends, err)
	}
	restarted := newServiceOn(t, f, database)
	if _, err := restarted.toolMessageSend(ctx, "server", "s1", payload); err == nil || sends != 1 {
		t.Fatalf("reconstruction resent a delivered intent: sends=%d err=%v", sends, err)
	}
	if g, err := restarted.messageGrant(ctx, "larkcli", "oc_example_group", "assistant_message", "bot"); err != nil || g == nil {
		t.Fatalf("restart lost grant: %+v %v", g, err)
	}
	if response := serve(restarted, "DELETE", "/api/v1/assistant/standing-authorizations/sa_messages", ""); response.Code != 200 {
		t.Fatal(response.Body)
	}
	if g, err := s.messageGrant(ctx, "larkcli", "oc_example_group", "assistant_message", "bot"); err != nil || g != nil {
		t.Fatalf("revoked grant cached: %+v %v", g, err)
	}
}
