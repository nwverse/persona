package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	chatRulesPath = "/api/v1/connectors/sent/chat-rules"
	sentPath      = "/api/v1/connectors/sent"
)

// chatRouteEnv is a manager with its gate running on the setting store (the one that runs for real), the fake GitHub
// connector, a ledger in memory with the rows a user would meet, and a computer "box" a node's gate can tell of a write.
type chatRouteEnv struct {
	h    *Manager
	st   *sentLedgerStore
	gh   *chatGitHub
	mux  http.Handler
	rows map[string]int64 // the ledger's rows by what they are
}

func chatRoutes(t *testing.T) *chatRouteEnv {
	t.Helper()
	ctx := context.Background()
	h := newTestManager(t)
	st := newSentLedgerStore()
	h.store = st
	box := newMachine(h, MachineConfig{ID: "box", Name: "box"})
	h.machines, h.byID["box"] = append(h.machines, box), box
	withLarkGates(t, h, chatLarkCommand)
	h.startConnectorGate(ctx)
	gh := &chatGitHub{}
	if err := h.addConnector(gh); err != nil {
		t.Fatal(err)
	}
	yes := true
	hubOf(h).answer = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &yes} }
	e := &chatRouteEnv{h: h, st: st, gh: gh, mux: h.routes(), rows: map[string]int64{}}
	add := func(name string, row db.SentAsUser) {
		row.Origin, row.LetThrough = json.RawMessage(`{}`), json.RawMessage(`{"kind":"card"}`)
		if row.Target == nil {
			row.Target = json.RawMessage(`{}`)
		}
		id, err := st.RecordSentAsUser(ctx, row)
		if err != nil {
			t.Fatal(err)
		}
		e.rows[name] = id
	}
	add("comment12", githubRowOf("github_comment", chatIssue12))
	add("comment13", githubRowOf("github_comment", chatIssue13))
	add("review", githubRowOf("github_review", chatReview))
	add("mail", db.SentAsUser{Connector: "mail", Action: "mail_send", Target: json.RawMessage(`{"to":["a@example.com"],"subject":"hi"}`)})
	add("recorded", db.SentAsUser{Connector: "example", Action: sentRecordAction, Target: json.RawMessage(`{"chat":"u1"}`)})
	add("other", db.SentAsUser{Connector: "example", Action: "example_send", Target: json.RawMessage(`{"chat":"u1"}`)})
	add("lark1", db.SentAsUser{Connector: "lark", Action: "POST /conversations/oc_1/messages", Target: json.RawMessage(`{"command":"feed","method":"POST","path":"/conversations/oc_1/messages","conversation":"oc_1"}`)})
	add("larkReply", db.SentAsUser{Connector: "lark", Action: "POST /messages/om_1/replies", Target: json.RawMessage(`{"command":"feed","method":"POST","path":"/messages/om_1/replies","message":"om_1"}`)})
	add("larkOtherGate", db.SentAsUser{Connector: "lark", Action: "POST /conversations/oc_2/messages",
		Target: json.RawMessage(`{"command":"othergate","method":"POST","path":"/conversations/oc_2/messages","conversation":"oc_2"}`)})
	return e
}

func (e *chatRouteEnv) master() string { return e.h.cfg.Token }

// rules is GET /chat-rules as the owner, the list as the app gets it.
func (e *chatRouteEnv) rules(t *testing.T) []map[string]any {
	t.Helper()
	code, out := call(t, e.mux, "GET", chatRulesPath, e.master(), "")
	if code != 200 {
		t.Fatalf("list: %d %v", code, out)
	}
	var list []map[string]any
	for _, r := range out["rules"].([]any) {
		list = append(list, r.(map[string]any))
	}
	return list
}

func (e *chatRouteEnv) post(t *testing.T, token, body string) (int, map[string]any) {
	t.Helper()
	return call(t, e.mux, "POST", chatRulesPath, token, body)
}

func (e *chatRouteEnv) row(name string) string { return fmt.Sprintf(`{"row": %d}`, e.rows[name]) }

// Who may do what. The list and the drop are for the user's own full devices and the owner on this machine; making a rule
// is for the user's own full devices only, not the owner's hub token (a rule lets writes through without a card, which
// the gate's bypass for what the user sends by hand also leaves to the devices). An answer-only device, a pairing token, an
// extension's token whatever its scopes and one that is none may do none of it. A refused request changes nothing. The
// same holds for what the ledger page says of the rules.
func TestChatRuleRoutesAreForTheUsersOwnDevices(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	pairing := "pair_" + randomHex(4)
	testDevices.Store(pairing, principal{ID: "pair", Role: roleFull, Paired: true})
	ext := e.h.extTokens.issue("example-x", "X", []string{extension.ScopeKBRead, extension.ScopeInboxRead, extension.ScopeAgentsRead})
	extAll := e.h.extTokens.issueAll("example-y", "Y")
	// reset leaves one rule, for #12, made by the phone: what there is to drop.
	reset := func() string {
		t.Helper()
		for _, r := range e.rules(t) {
			if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+r["id"].(string), phone, ""); code != 200 {
				t.Fatalf("resetting: %d", code)
			}
		}
		code, out := e.post(t, phone, e.row("comment12"))
		if code != 200 {
			t.Fatalf("setting up: %d %v", code, out)
		}
		return out["rule"].(map[string]any)["id"].(string)
	}
	for name, c := range map[string]struct {
		token                    string
		list, make, drop, ledger int // the status each is answered with
	}{
		"the owner on this machine":     {e.master(), 200, 403, 200, 200},
		"a full device":                 {phone, 200, 200, 200, 200},
		"an answer-only device":         {deviceToken("dev_watch", roleAnswer), 403, 403, 403, 403},
		"a pairing token":               {pairing, 403, 403, 403, 403},
		"an extension's token":          {ext, 403, 403, 403, 403},
		"an extension with every scope": {extAll, 403, 403, 403, 403},
		"a token nobody knows":          {"nobody", 401, 401, 401, 401},
	} {
		key := reset()
		codeGet, _ := call(t, e.mux, "GET", chatRulesPath, c.token, "")
		codePost, _ := e.post(t, c.token, e.row("lark1"))
		hasLark := slices.ContainsFunc(e.rules(t), func(r map[string]any) bool { return r["chat"] == "oc_1" })
		codeDel, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, c.token, "")
		stillThere := slices.ContainsFunc(e.rules(t), func(r map[string]any) bool { return r["id"] == key })
		codeLedger, ledger := call(t, e.mux, "GET", sentPath, c.token, "")
		if codeGet != c.list || codePost != c.make || codeDel != c.drop || codeLedger != c.ledger {
			t.Errorf("%s: list %d, make %d, drop %d, ledger %d; want %d, %d, %d, %d", name, codeGet, codePost, codeDel, codeLedger, c.list, c.make, c.drop, c.ledger)
		}
		if hasLark != (c.make == 200) {
			t.Errorf("%s: make answered %d and the rule for the Lark chat exists: %v", name, codePost, hasLark)
		}
		if stillThere != (c.drop != 200) {
			t.Errorf("%s: drop answered %d and the rule is still there: %v", name, codeDel, stillThere)
		}
		if c.ledger == 200 && ledger["items"] == nil {
			t.Errorf("%s: the ledger page has no items", name)
		}
		// What the gate holds is what the rules are: a refused request changed nothing in it either.
		if got, want := len(e.h.connectors.UserRules()), len(e.rules(t)); got != want {
			t.Errorf("%s: the gate has %d rules, there are %d", name, got, want)
		}
	}
	// The handlers check too, whatever reached them: the same principals, without the front door's help.
	as := func(p principal, method, path, body string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		e.h.apiMux.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), p)))
		return rec.Code
	}
	reset()
	for name, p := range map[string]principal{
		"an answer-only device": {ID: "dev_watch", Role: roleAnswer},
		"an extension":          {ID: "ext_example", Role: roleFull, Extension: "example"},
		"a pairing token":       {ID: "tok_script", Role: roleFull, Paired: true},
		"no one":                {},
	} {
		for _, req := range []struct{ method, path, body string }{
			{"GET", chatRulesPath, ""}, {"POST", chatRulesPath, e.row("lark1")}, {"DELETE", chatRulesPath + "/cr_0000000000000000", ""},
		} {
			if code := as(p, req.method, req.path, req.body); code != 403 {
				t.Errorf("%s: %s %s: %d", name, req.method, req.path, code)
			}
		}
	}
	if code := as(principal{ID: "dev_phone", Role: roleFull}, "POST", chatRulesPath, e.row("lark1")); code != 200 {
		t.Errorf("a full device makes a rule: %d", code)
	}
	if code := as(principal{ID: "dev_phone", Role: roleFull}, "GET", chatRulesPath, ""); code != 200 {
		t.Errorf("a full device lists: %d", code)
	}
	owner := principal{Role: roleFull, Owner: true}
	if code := as(owner, "GET", chatRulesPath, ""); code != 200 {
		t.Errorf("the owner lists: %d", code)
	}
	if code := as(owner, "DELETE", chatRulesPath+"/cr_0000000000000000", ""); code != 404 { // let in, and there is no such rule
		t.Errorf("the owner drops: %d", code)
	}
	before := len(e.rules(t))
	if code := as(owner, "POST", chatRulesPath, e.row("comment13")); code != 403 || len(e.rules(t)) != before {
		t.Errorf("the owner makes a rule: %d, %d rules after %d", code, len(e.rules(t)), before)
	}
}

// A rule is made from a row of the ledger by the id of the row: the chat's rule, once however many times it is asked, with
// what the app lists. The next write into that chat goes without a card, any other still asks; dropping the rule asks
// again at the next write, for the connectors' registry and for what the node's Lark gate is served.
func TestChatRuleFromARowAndDroppingIt(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	servedLark := func() []proto.LarkRule {
		lk, err := e.h.assistantLark(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return lk.Rules
	}
	if !chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Fatal("asked nothing with no rule")
	}

	code, out := e.post(t, phone, e.row("comment12"))
	rule, _ := out["rule"].(map[string]any)
	if code != 200 || out["created"] != true || rule["connector"] != "github" || rule["chat"] != "example-org/example-repo#12" ||
		rule["row"] != float64(e.rows["comment12"]) || rule["at"] == nil || !strings.HasPrefix(fmt.Sprint(rule["id"]), "cr_") ||
		!strings.Contains(fmt.Sprint(rule["summary"]), "github_comment") {
		t.Fatalf("make: %d %v", code, out)
	}
	key := rule["id"].(string)
	code, again := e.post(t, phone, e.row("comment12"))
	if code != 200 || again["created"] != false || again["rule"].(map[string]any)["id"] != key || len(e.rules(t)) != 1 {
		t.Fatalf("make again: %d %v, %d rules", code, again, len(e.rules(t)))
	}
	if list := e.rules(t); len(list) != 1 || list[0]["id"] != key || list[0]["row"] != rule["row"] {
		t.Fatalf("list: %v", list)
	}
	if code, out := call(t, e.mux, "GET", chatRulesPath, phone, ""); code != 200 || out["most"] != float64(chatRulesMost) || chatRulesMost != 500 {
		t.Errorf("the most there can be: %d %v", code, out)
	}
	// An id is the whole of one: a part of it, or something that is none, drops nothing.
	for _, bad := range []string{"cr_", key[:6], key + "0", strings.ToUpper(key), "x", "%20"} {
		if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+bad, phone, ""); code != 404 || len(e.rules(t)) != 1 {
			t.Errorf("DELETE %q: %d, %d rules left", bad, code, len(e.rules(t)))
		}
	}
	if chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Error("the chat of the rule was asked about")
	}
	if !chatWrite(t, e.h, e.gh, "github_comment", chatIssue13) || !chatWrite(t, e.h, e.gh, "github_review", chatReview) {
		t.Error("another issue, or a review of the chat, was not asked about")
	}

	// The same row can be asked for a rule made of Lark rows: served to the node on its next pull.
	if code, out := e.post(t, phone, e.row("lark1")); code != 200 || out["created"] != true {
		t.Fatalf("a Lark row: %d %v", code, out)
	}
	if got := servedLark(); !reflect.DeepEqual(got, []proto.LarkRule{{Method: "POST", Path: "/conversations/oc_1/messages", Action: "allow"}}) {
		t.Fatalf("served %+v", got)
	}
	if list := e.rules(t); len(list) != 2 || list[0]["connector"] != "lark" || list[0]["chat"] != "oc_1" || list[1]["id"] != key {
		t.Fatalf("newest first: %v", list)
	}

	// Dropped: asked again at the next write, and the node is served none.
	if code, out := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, ""); code != 200 || out["ok"] != true {
		t.Fatalf("drop: %d %v", code, out)
	}
	if !chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Error("a dropped rule still let the chat through")
	}
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, ""); code != 404 {
		t.Errorf("drop again: %d", code)
	}
	larkKey := e.rules(t)[0]["id"].(string)
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+larkKey, phone, ""); code != 200 || servedLark() != nil || len(e.h.connectors.UserRules()) != 0 {
		t.Errorf("drop the Lark rule: %d, served %+v, the gate has %d", code, servedLark(), len(e.h.connectors.UserRules()))
	}
	if list := e.rules(t); len(list) != 0 {
		t.Errorf("list after dropping them: %v", list)
	}
}

// The request names a row and nothing else: a client cannot hand over a rule, however it is shaped, and a row that gives
// none says why, with the reason of connector.NoChat… Nothing is kept for a request that failed.
func TestChatRuleRequestsCarryNoRule(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	for name, body := range map[string]string{
		"no body":                    ``,
		"not JSON":                   `row 1`,
		"an empty object":            `{}`,
		"row 0":                      `{"row": 0}`,
		"a negative row":             `{"row": -3}`,
		"a text for the row":         `{"row": "1"}`,
		"a rule beside the row":      fmt.Sprintf(`{"row": %d, "rule": {"connector":"lark","action":"POST /conversations/oc_9/messages","decision":"allow"}}`, e.rows["comment12"]),
		"a rule and no row":          `{"rule": {"connector":"*","action":"*","decision":"allow"}}`,
		"a connector and an action":  fmt.Sprintf(`{"row": %d, "connector": "*", "action": "*"}`, e.rows["comment12"]),
		"a chat beside the row":      fmt.Sprintf(`{"row": %d, "chat": "example-org/other#1"}`, e.rows["comment12"]),
		"a second document":          fmt.Sprintf(`{"row": %d} {"row": %d}`, e.rows["comment12"], e.rows["comment13"]),
		"a closing brace after it":   fmt.Sprintf(`{"row": %d}}`, e.rows["comment12"]),
		"a closing bracket after it": fmt.Sprintf(`{"row": %d}]`, e.rows["comment12"]),
		"text after it":              fmt.Sprintf(`{"row": %d} please`, e.rows["comment12"]),
		"two documents":              fmt.Sprintf(`{"row": %d}{"row": %d}`, e.rows["comment12"], e.rows["comment13"]),
	} {
		if code, _ := e.post(t, phone, body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if list := e.rules(t); len(list) != 0 || len(e.h.connectors.UserRules()) != 0 {
		t.Errorf("rules after the bad requests: %v, the gate has %+v", list, e.h.connectors.UserRules())
	}
	if code, _ := e.post(t, phone, `{"row": 1`+strings.Repeat(" ", 5000)+`}`); code != 413 {
		t.Errorf("a body that is too large: %d", code)
	}
}

// A Lark row whose target does not say a command of the Lark gate is refused over the route like any row that offers no
// rule (409 no_chat_rule, reason command) and the ledger page says so, whatever the target is made of; nothing is kept.
func TestChatRuleOfALarkRowWithNoReadableCommandIsRefused(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	rows := map[string]int64{}
	for name, target := range map[string]string{
		"null": `null`, "an empty object": `{}`, "an empty list": `[]`, "a number": `7`,
		"no command": `{"conversation":"oc_7"}`, "an empty command": `{"command":"","conversation":"oc_7"}`, "a command that is not text": `{"command":3,"conversation":"oc_7"}`,
	} {
		id, err := e.st.RecordSentAsUser(ctx, db.SentAsUser{Connector: "lark", Action: "POST /conversations/oc_7/messages", Target: json.RawMessage(target),
			Origin: json.RawMessage(`{}`), LetThrough: json.RawMessage(`{"kind":"card"}`)})
		if err != nil {
			t.Fatal(err)
		}
		rows[name] = id
		if code, out := e.post(t, phone, fmt.Sprintf(`{"row": %d}`, id)); code != 409 || out["code"] != "no_chat_rule" || out["reason"] != "command" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	_, ledger := call(t, e.mux, "GET", sentPath, phone, "")
	seen := 0
	for _, it := range ledger["items"].([]any) {
		row := it.(map[string]any)
		for name, id := range rows {
			if int64(row["id"].(float64)) == id {
				seen++
				if cr := row["chat_rule"].(map[string]any); cr["can"] != false || cr["reason"] != "command" {
					t.Errorf("%s: the ledger page says %v", name, cr)
				}
			}
		}
	}
	if seen != len(rows) {
		t.Errorf("the ledger page showed %d of %d rows", seen, len(rows))
	}
	if list := e.rules(t); len(list) != 0 || len(e.h.connectors.UserRules()) != 0 {
		t.Errorf("kept %v", list)
	}
	// The row that names the gate's command, for the same conversation, is the control.
	if code, out := e.post(t, phone, e.row("lark1")); code != 200 {
		t.Errorf("the control row: %d %v", code, out)
	}
}

// A row that offers no rule answers 409 no_chat_rule with the reason (the code of connector.NoChatRuleError); a row that is
// not there 404; and none of them keeps anything.
func TestChatRuleFromARowThatGivesNone(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	for name, reason := range map[string]string{"review": connector.NoChatReview, "mail": connector.NoChatMail, "recorded": connector.NoChatRecorded,
		"other": connector.NoChatUnknown, "larkReply": connector.NoChatReply, "larkOtherGate": connector.NoChatCommand} {
		code, out := e.post(t, phone, e.row(name))
		if code != 409 || out["code"] != "no_chat_rule" || out["reason"] != reason || out["error"] == "" {
			t.Errorf("%s: %d %v; want reason %s", name, code, out, reason)
		}
	}
	if code, out := e.post(t, phone, `{"row": 9999}`); code != 404 || out["code"] != "not_found" {
		t.Errorf("a row that is not there: %d %v", code, out)
	}
	if list := e.rules(t); len(list) != 0 || len(e.h.connectors.UserRules()) != 0 {
		t.Errorf("kept %v after rows that give none", list)
	}
}

// At the limit one more rule is refused, 409 too_many_rules; a chat that has a rule is still answered, and dropping one
// makes room.
func TestChatRuleLimitOverTheRoute(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	var full []connector.UserRule
	for n := 100; n < 100+chatRulesMost; n++ {
		full = append(full, ghUserRule(n))
	}
	if err := e.st.PutSetting(ctx, chatRulesKey, full); err != nil {
		t.Fatal(err)
	}
	e.h.userRules.loaded = false // as after a restart: the rules are read from the setting
	if err := e.h.loadUserRules(ctx); err != nil {
		t.Fatal(err)
	}
	phone := deviceToken("dev_phone", roleFull)
	if code, out := e.post(t, phone, e.row("comment12")); code != 409 || out["code"] != "too_many_rules" {
		t.Fatalf("the %dst: %d %v", chatRulesMost+1, code, out)
	}
	if list := e.rules(t); len(list) != chatRulesMost || len(e.h.connectors.UserRules()) != chatRulesMost {
		t.Fatalf("%d rules, the gate has %d", len(list), len(e.h.connectors.UserRules()))
	}
	oldest := e.rules(t)[chatRulesMost-1]["id"].(string)
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+oldest, phone, ""); code != 200 {
		t.Fatal(code)
	}
	if code, out := e.post(t, phone, e.row("comment12")); code != 200 || out["created"] != true {
		t.Fatalf("with room: %d %v", code, out)
	}
}

// Every row of the ledger page says whether a rule can be made from it and whether its chat has one (chat_rule); the
// rest of the row is what it was.
func TestLedgerRowsSayWhetherARuleCanBeMade(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	read := func() map[int64]map[string]any {
		code, out := call(t, e.mux, "GET", sentPath, phone, "")
		if code != 200 {
			t.Fatalf("ledger: %d %v", code, out)
		}
		rows := map[int64]map[string]any{}
		for _, it := range out["items"].([]any) {
			row := it.(map[string]any)
			rows[int64(row["id"].(float64))] = row
			for _, k := range []string{"id", "at", "connector", "action", "target", "text", "summary", "origin", "let_through", "can_recall"} {
				if _, ok := row[k]; !ok {
					t.Fatalf("a row lost %q: %v", k, row)
				}
			}
		}
		return rows
	}
	state := func(rows map[int64]map[string]any, name string) map[string]any {
		return rows[e.rows[name]]["chat_rule"].(map[string]any)
	}
	rows := read()
	for name, want := range map[string]map[string]any{
		"comment12":     {"can": true, "exists": false},
		"comment13":     {"can": true, "exists": false},
		"lark1":         {"can": true, "exists": false},
		"review":        {"can": false, "exists": false, "reason": "review"},
		"mail":          {"can": false, "exists": false, "reason": "mail"},
		"recorded":      {"can": false, "exists": false, "reason": "recorded"},
		"other":         {"can": false, "exists": false, "reason": "unknown"},
		"larkReply":     {"can": false, "exists": false, "reason": "reply"},
		"larkOtherGate": {"can": false, "exists": false, "reason": "command"},
	} {
		if got := state(rows, name); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: chat_rule %v, want %v", name, got, want)
		}
	}
	// With a rule for #12: its row, and another row of the same chat, say so and give the rule's id; the rest do not.
	_, made := e.post(t, phone, e.row("comment12"))
	key := made["rule"].(map[string]any)["id"].(string)
	sameChat, err := e.st.RecordSentAsUser(context.Background(), githubRowOf("github_comment", chatIssue12))
	if err != nil {
		t.Fatal(err)
	}
	rows = read()
	for _, id := range []int64{e.rows["comment12"], sameChat} {
		if got := rows[id]["chat_rule"].(map[string]any); got["can"] != true || got["exists"] != true || got["id"] != key {
			t.Errorf("row %d of the chat with a rule: %v", id, got)
		}
	}
	if got := state(rows, "comment13"); got["exists"] != false || got["id"] != nil {
		t.Errorf("a row of another chat: %v", got)
	}
	// Dropped, they say so again.
	call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, "")
	if got := state(read(), "comment12"); got["can"] != true || got["exists"] != false {
		t.Errorf("after dropping the rule: %v", got)
	}
}

// With no ledger or no database the routes say so (503 no_database) and keep nothing; a manager that has the settings but
// no ledger lists no rules.
func TestChatRuleRoutesWithoutADatabase(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	e.h.store = newKeepStore() // settings, no ledger
	if code, out := call(t, e.mux, "GET", chatRulesPath, phone, ""); code != 200 || len(out["rules"].([]any)) != 0 {
		t.Errorf("no ledger, list: %d %v", code, out)
	}
	if code, out := e.post(t, phone, e.row("comment12")); code != 503 || out["code"] != "no_database" {
		t.Errorf("no ledger, make: %d %v", code, out)
	}
	e.h.store = nil
	e.h.userRules.loaded = false
	e.h.userRules.snap.Store(nil)
	for _, req := range []struct{ method, path, body string }{{"GET", chatRulesPath, ""}, {"POST", chatRulesPath, e.row("comment12")}, {"DELETE", chatRulesPath + "/cr_0000000000000000", ""}} {
		if code, out := call(t, e.mux, req.method, req.path, phone, req.body); code != 503 || out["code"] != "no_database" {
			t.Errorf("no database: %s: %d %v", req.method, code, out)
		}
	}
}

// What the ledger says of a write a user's rule let through: let_through is still of kind "rule", with the rule, and says
// user_rule: true; a hand-written rule's, a card's and a Lark rule of the same shape written by hand are not.
func TestLedgerTellsWritesOfTheUsersRulesFromHandWrittenOnes(t *testing.T) {
	ctx := context.Background()
	hand := []connector.Rule{{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/hand"}, Decision: connector.Allow}}
	st := &memRuleStore{}
	h, gh := chatRuleManager(t, hand, st)
	if _, _, err := h.addChatRule(ctx, githubRowOf("github_comment", chatIssue12)); err != nil {
		t.Fatal(err)
	}
	last := func() map[string]any {
		t.Helper()
		rows := h.store.(*sentLedgerStore).all()
		return objectOf(t, rows[len(rows)-1].LetThrough)
	}
	chatWrite(t, h, gh, "github_comment", chatIssue12)
	if through := last(); through["kind"] != "rule" || through["user_rule"] != true || objectOf(t, mustJSON(through["rule"]))["connector"] != "github" {
		t.Errorf("by the user's rule: %v", through)
	}
	chatWrite(t, h, gh, "github_comment", `{"repo":"example-org/hand","number":3,"body":"x"}`)
	if through := last(); through["kind"] != "rule" || through["user_rule"] != nil || len(through) != 2 {
		t.Errorf("by a hand-written rule: %v", through)
	}
	chatWrite(t, h, gh, "github_comment", chatIssue13)
	if through := last(); through["kind"] != "card" || through["user_rule"] != nil {
		t.Errorf("by a card: %v", through)
	}

	// The node's Lark gate tells the rule that let a write through; it is the user's when it is the same as one of theirs
	// and no hand-written rule is the same too.
	if _, _, err := h.addChatRule(ctx, db.SentAsUser{Connector: "lark", Action: "POST /conversations/oc_1/messages", Target: json.RawMessage(`{"command":"feed","conversation":"oc_1"}`)}); err != nil {
		t.Fatal(err)
	}
	node := func(path string, kind string, rule *proto.LarkRule) map[string]any {
		t.Helper()
		if _, err := h.RecordNodeSent(ctx, "box", larkSentParams(t, proto.LarkSent{Method: "POST", Path: path,
			LetThrough: proto.LarkLetThrough{Kind: kind, Rule: rule}}, nil)); err != nil {
			t.Fatal(err)
		}
		return last()
	}
	users := &proto.LarkRule{Method: "POST", Path: "/conversations/oc_1/messages", Action: "allow"}
	if through := node("/conversations/oc_1/messages", proto.LarkThroughRule, users); through["user_rule"] != true || through["kind"] != "rule" {
		t.Errorf("the node, by the user's rule: %v", through)
	}
	withBody := &proto.LarkRule{Method: "POST", Path: "/conversations/oc_1/messages", Body: map[string]any{"type": "p2p"}, Action: "allow"}
	if through := node("/conversations/oc_1/messages", proto.LarkThroughRule, withBody); through["user_rule"] != nil {
		t.Errorf("the node, by a hand-written rule of the same path that looks into the body: %v", through)
	}
	other := &proto.LarkRule{Method: "POST", Path: "/conversations/*/messages", Action: "allow"}
	if through := node("/conversations/oc_5/messages", proto.LarkThroughRule, other); through["user_rule"] != nil {
		t.Errorf("the node, by another rule: %v", through)
	}
	if through := node("/conversations/oc_1/messages", proto.LarkThroughAlways, nil); through["user_rule"] != nil {
		t.Errorf("the node, by always: %v", through)
	}
	h.cfg.LarkGate = []proto.LarkRule{{Method: "POST", Path: "/conversations/oc_1/messages", Action: "allow"}}
	if through := node("/conversations/oc_1/messages", proto.LarkThroughRule, users); through["user_rule"] != nil {
		t.Errorf("the node, by a rule that is the same as one written by hand: %v", through)
	}
}

// A rule the store would not drop is off in the gate and still stored; asking again finishes the removal (the id the app
// has still names it), and it does not come back. A store that refuses again is answered as it was the first time
// (store_failed), not as an error of the manager's own. The audit has the rule going off at the first request, and the
// request that finished it; not the one that failed with nothing to change.
func TestChatRuleDropAskedAgainAfterTheStoreFailed(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	aud := newAuditingStore()
	flaky := &failingWrites{sentLedgerStore: e.st, audit: aud}
	e.h.store = flaky
	_, made := e.post(t, phone, e.row("comment12"))
	key := made["rule"].(map[string]any)["id"].(string)
	if chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Fatal("the rule is not in force")
	}
	flaky.failWrites.Store(true)
	for _, try := range []string{"the first time", "the second time"} {
		if code, out := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, ""); code != 500 || out["code"] != "store_failed" {
			t.Fatalf("the store refuses %s: %d %v", try, code, out)
		}
		if !chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
			t.Errorf("the rule went on allowing after its removal was asked for (%s)", try)
		}
	}
	flaky.failWrites.Store(false)
	if code, out := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, ""); code != 200 || out["ok"] != true {
		t.Fatalf("asked again: %d %v", code, out)
	}
	var kept []connector.UserRule
	if _, err := e.st.Setting(ctx, chatRulesKey, &kept); err != nil || len(kept) != 0 {
		t.Fatalf("still stored: %v, %v", idsOf(kept), err)
	}
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, ""); code != 404 {
		t.Errorf("a third time: %d", code)
	}
	if got := actions(aud.take()); !slices.Equal(got, []string{auditConnectorChatRuleAdded, auditConnectorChatRuleRemoved, auditConnectorChatRuleRemoved}) {
		t.Errorf("the audit: %v", got)
	}
}

// failingWrites is a store whose writes can be made to fail.
type failingWrites struct {
	*sentLedgerStore
	failWrites atomic.Bool
	audit      *auditingStore // keeps the audit when set
}

func (s *failingWrites) AddExtensionAudit(ctx context.Context, id, action, actor string, detail json.RawMessage) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.AddExtensionAudit(ctx, id, action, actor, detail)
}

func (s *failingWrites) PutSetting(ctx context.Context, key string, v any) error {
	if s.failWrites.Load() {
		return errors.New("the disk is full")
	}
	return s.sentLedgerStore.PutSetting(ctx, key, v)
}

// failingReads is a ledger store whose reads of the rules' setting can fail.
type failingReads struct {
	*sentLedgerStore
	fail atomic.Bool
	hang atomic.Bool
}

func (s *failingReads) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == chatRulesKey && s.fail.Load() {
		return false, errors.New("the database went away")
	}
	if key == chatRulesKey && s.hang.Load() {
		return false, hangUntil(ctx)
	}
	return s.sentLedgerStore.Setting(ctx, key, out)
}

// With the rules unreadable the list and a change say so (500) and write nothing, while the ledger page goes on: no row says
// it has a rule (the gate has none either), the rest of it as it was. Once they can be read it is all as it should be.
func TestChatRuleRoutesWhenTheRulesCannotBeRead(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	flaky := &failingReads{sentLedgerStore: e.st}
	e.h.store = flaky
	e.h.userRules.loaded = false // as at a start that could not read them
	e.h.userRules.snap.Store(nil)
	flaky.fail.Store(true)
	if code, _ := call(t, e.mux, "GET", chatRulesPath, phone, ""); code != 500 {
		t.Errorf("the list: %d", code)
	}
	if code, _ := e.post(t, phone, e.row("comment12")); code != 500 {
		t.Errorf("a change: %d", code)
	}
	code, out := call(t, e.mux, "GET", sentPath, phone, "")
	if code != 200 || len(out["items"].([]any)) != len(e.rows) {
		t.Fatalf("the ledger page: %d %v", code, out)
	}
	for _, it := range out["items"].([]any) {
		if cr := it.(map[string]any)["chat_rule"].(map[string]any); cr["exists"] != false {
			t.Errorf("a row says it has a rule: %v", cr)
		}
	}
	var kept []connector.UserRule
	if _, err := e.st.Setting(context.Background(), chatRulesKey, &kept); err != nil || len(kept) != 0 {
		t.Errorf("something was written: %v %v", kept, err)
	}
	flaky.fail.Store(false)
	if code, out := e.post(t, phone, e.row("comment12")); code != 200 || out["created"] != true || len(e.rules(t)) != 1 {
		t.Errorf("once they can be read: %d %v", code, out)
	}
}

// The first read of the rules (at start it failed, so the ledger page or the list reads them) is held to the time limit a
// change has: a store that hangs there costs the ledger page, and the answer of a recall, which is a row of it, no more
// than that, and the page still answers, no row saying it has a rule.
func TestTheFirstReadOfTheRulesIsHeldToTheTimeLimit(t *testing.T) {
	shortStoreTime(t)
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	hanging := &failingReads{sentLedgerStore: e.st}
	e.h.store = hanging
	e.h.userRules.loaded = false // as at a start that could not read them
	e.h.userRules.snap.Store(nil)
	hanging.hang.Store(true)
	start := time.Now()
	code, out := call(t, e.mux, "GET", sentPath, phone, "")
	if d := time.Since(start); d > time.Second || code != 200 {
		t.Fatalf("the ledger page: %d after %v", code, d)
	}
	for _, it := range out["items"].([]any) {
		if cr := it.(map[string]any)["chat_rule"].(map[string]any); cr["exists"] != false {
			t.Errorf("a row says it has a rule: %v", cr)
		}
	}
	start = time.Now()
	if code, _ := call(t, e.mux, "GET", chatRulesPath, phone, ""); time.Since(start) > time.Second || code != 500 {
		t.Errorf("the list: %d after %v", code, time.Since(start))
	}
	hanging.hang.Store(false)
	if code, out := e.post(t, phone, e.row("comment12")); code != 200 || out["created"] != true {
		t.Errorf("once they can be read: %d %v", code, out)
	}
}

// A row taken back still offers the rule of its chat, and the rule made from it works: the rule is for the chat, not for
// the message, and a row of the same chat not taken back offers the same one. A client that would rather not offer it
// next to a message taken back can tell by recalled_at.
func TestARowTakenBackStillOffersTheRuleOfItsChat(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	if ok, err := e.st.MarkSentAsUserRecalled(ctx, e.rows["comment12"], time.Now()); !ok || err != nil {
		t.Fatalf("mark: %v %v", ok, err)
	}
	code, out := call(t, e.mux, "GET", sentPath, phone, "")
	if code != 200 {
		t.Fatalf("the ledger page: %d %v", code, out)
	}
	for _, it := range out["items"].([]any) {
		row := it.(map[string]any)
		if row["id"] != float64(e.rows["comment12"]) {
			continue
		}
		if cr := row["chat_rule"].(map[string]any); row["recalled_at"] == nil || row["can_recall"] != false || cr["can"] != true {
			t.Fatalf("the row taken back: %v", row)
		}
	}
	if code, out := e.post(t, phone, e.row("comment12")); code != 200 || out["created"] != true {
		t.Fatalf("a rule from it: %d %v", code, out)
	}
	if chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Error("the chat of the rule was asked about")
	}
}

// blockingWrites is a ledger store whose write of the rules' setting waits to be let go (or for the call's own end).
type blockingWrites struct {
	*sentLedgerStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingWrites) PutSetting(ctx context.Context, key string, v any) error {
	if key == chatRulesKey {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.sentLedgerStore.PutSetting(ctx, key, v)
}

// A change that waits on the database does not hold up the ledger page or the list: they are read from what the manager
// holds, and answer while the change is still being written.
func TestLedgerPageDoesNotWaitForAChangeInTheStore(t *testing.T) {
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	blocking := &blockingWrites{sentLedgerStore: e.st, entered: make(chan struct{}), release: make(chan struct{})}
	e.h.store = blocking
	done := make(chan int, 1)
	go func() {
		code, _ := e.post(t, phone, e.row("comment12"))
		done <- code
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the change never reached the store")
	}
	answered := make(chan [2]int, 1)
	go func() {
		a, _ := call(t, e.mux, "GET", sentPath, phone, "")
		b, _ := call(t, e.mux, "GET", chatRulesPath, phone, "")
		answered <- [2]int{a, b}
	}()
	select {
	case got := <-answered:
		if got != [2]int{200, 200} {
			t.Errorf("the ledger page and the list while a change waits: %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Error("the ledger page or the list waited for a change in the store")
	}
	close(blocking.release)
	if code := <-done; code != 200 {
		t.Errorf("the change: %d", code)
	}
}

// What a hand-edited setting holds that no gate could run (a confirm, a pattern, a duplicate) stays in it and is neither
// listed nor said to be a rule of its chat.
func TestChatRuleListShowsOnlyWhatTheGateRuns(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	confirm13 := ghUserRule(13)
	confirm13.Rule.Decision = connector.Confirm
	if err := e.st.PutSetting(ctx, chatRulesKey, []connector.UserRule{
		ghUserRule(12), confirm13, {Rule: connector.Rule{Connector: "*", Action: "*", Decision: connector.Allow}}, ghUserRule(12)}); err != nil {
		t.Fatal(err)
	}
	if err := e.h.loadUserRules(ctx); err != nil {
		t.Fatal(err)
	}
	if list := e.rules(t); len(list) != 1 || list[0]["chat"] != "example-org/example-repo#12" {
		t.Fatalf("listed %v", list)
	}
	_, out := call(t, e.mux, "GET", sentPath, phone, "")
	for _, it := range out["items"].([]any) {
		row := it.(map[string]any)
		cr := row["chat_rule"].(map[string]any)
		switch int64(row["id"].(float64)) {
		case e.rows["comment12"]:
			if cr["exists"] != true {
				t.Errorf("#12: %v", cr)
			}
		case e.rows["comment13"]:
			if cr["exists"] != false {
				t.Errorf("#13, whose rule the setting holds as a confirm: %v", cr)
			}
		}
	}
	// A change keeps what it did not understand: the other entries are not lost.
	if code, _ := e.post(t, phone, e.row("lark1")); code != 200 {
		t.Fatal(code)
	}
	var kept []connector.UserRule
	if _, err := e.st.Setting(ctx, chatRulesKey, &kept); err != nil || len(kept) != 5 {
		t.Errorf("the setting holds %d entries after a change: %v", len(kept), err)
	}
}

// chatRuleAuditStore is the ledger in memory that also keeps the audit (plugin_audit).
type chatRuleAuditStore struct {
	*sentLedgerStore
	audit *auditingStore
}

func (a chatRuleAuditStore) AddExtensionAudit(ctx context.Context, id, action, actor string, detail json.RawMessage) error {
	return a.audit.AddExtensionAudit(ctx, id, action, actor, detail)
}

// A rule made and a rule dropped are recorded in plugin_audit like a sign-in or a switch: the connector in the plugin
// column, who asked (the device, or the hub token for the owner) as the actor, the chat and the ledger row it came from as
// the detail, and never the words of the row. Nothing is recorded for what changed nothing (a chat that has a rule asked for
// again, a refusal, a row that is not there, a rule that is not there), and a record that cannot be written does not undo
// the change.
func TestChatRuleChangesAreAudited(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	aud := newAuditingStore()
	e.h.store = chatRuleAuditStore{e.st, aud}
	phone := deviceToken("dev_phone", roleFull)
	watch := deviceToken("dev_watch", roleAnswer)
	const secret = "SECRET-WORDS-4711"
	words, err := e.st.RecordSentAsUser(ctx, db.SentAsUser{Connector: "github", Action: "github_comment", Text: secret, Summary: secret + " on #77",
		Target: json.RawMessage(`{"ref":"example-org/example-repo#77"}`), Origin: json.RawMessage(`{}`), LetThrough: json.RawMessage(`{"kind":"card"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var all []auditRow
	taken := func() []auditRow {
		rows := aud.take()
		all = append(all, rows...)
		return rows
	}
	only := func(what string, rows []auditRow, connector, action, actor string, detail map[string]any) {
		t.Helper()
		if len(rows) != 1 || rows[0].ID != connector || rows[0].Action != action || rows[0].Actor != actor || !reflect.DeepEqual(rows[0].Detail, detail) {
			t.Errorf("%s: recorded %+v, want %s %s by %s %v", what, rows, connector, action, actor, detail)
		}
	}

	// What changes nothing records nothing.
	for name, req := range map[string]struct{ token, body string }{
		"the owner":               {e.master(), fmt.Sprintf(`{"row": %d}`, words)},
		"an answer-only device":   {watch, fmt.Sprintf(`{"row": %d}`, words)},
		"a row that gives none":   {phone, e.row("mail")},
		"a row that is not there": {phone, `{"row": 9999}`},
		"no row":                  {phone, `{}`},
	} {
		if code, _ := e.post(t, req.token, req.body); code == 200 {
			t.Fatalf("%s: made a rule", name)
		}
	}
	if rows := taken(); len(rows) != 0 {
		t.Fatalf("recorded what made nothing: %+v", rows)
	}

	// Made: the connector, the device, the row and the chat.
	if code, out := e.post(t, phone, fmt.Sprintf(`{"row": %d}`, words)); code != 200 || out["created"] != true {
		t.Fatalf("make: %d %v", code, out)
	}
	only("a GitHub rule", taken(), "github", auditConnectorChatRuleAdded, "device:dev_phone",
		map[string]any{"row": float64(words), "chat": "example-org/example-repo#77"})
	if code, out := e.post(t, phone, fmt.Sprintf(`{"row": %d}`, words)); code != 200 || out["created"] != false {
		t.Fatalf("make again: %d %v", code, out)
	}
	if rows := taken(); len(rows) != 0 {
		t.Fatalf("recorded a rule that was there already: %+v", rows)
	}
	if code, out := e.post(t, phone, e.row("lark1")); code != 200 {
		t.Fatalf("make a Lark rule: %d %v", code, out)
	}
	only("a Lark rule", taken(), "lark", auditConnectorChatRuleAdded, "device:dev_phone", map[string]any{"row": float64(e.rows["lark1"]), "chat": "oc_1"})

	// Dropped: by the owner this time, and the rule's own row and chat.
	var githubKey string
	for _, r := range e.rules(t) {
		if r["chat"] == "example-org/example-repo#77" {
			githubKey = r["id"].(string)
		}
	}
	for name, req := range map[string]struct{ token, key string }{
		"an answer-only device": {watch, githubKey}, "an id that is none": {phone, "cr_0000000000000000"},
	} {
		if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+req.key, req.token, ""); code == 200 {
			t.Fatalf("%s: dropped a rule", name)
		}
	}
	if rows := taken(); len(rows) != 0 {
		t.Fatalf("recorded a drop that dropped nothing: %+v", rows)
	}
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+githubKey, e.master(), ""); code != 200 {
		t.Fatal(code)
	}
	only("a drop by the owner", taken(), "github", auditConnectorChatRuleRemoved, "hub token",
		map[string]any{"row": float64(words), "chat": "example-org/example-repo#77"})
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+githubKey, e.master(), ""); code != 404 {
		t.Fatal(code)
	}
	if rows := taken(); len(rows) != 0 {
		t.Fatalf("recorded a second drop of the same rule: %+v", rows)
	}
	larkKey := e.rules(t)[0]["id"].(string)
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+larkKey, phone, ""); code != 200 {
		t.Fatal(code)
	}
	only("a drop by a device", taken(), "lark", auditConnectorChatRuleRemoved, "device:dev_phone", map[string]any{"row": float64(e.rows["lark1"]), "chat": "oc_1"})

	// The words of the row are nowhere in what was recorded.
	if raw, _ := json.Marshal(all); len(all) != 4 || strings.Contains(string(raw), secret) {
		t.Errorf("the audit has %d records, and the row's words in them: %s", len(all), raw)
	}

	// A record that cannot be written does not undo the change.
	aud.mu.Lock()
	aud.fail = true
	aud.mu.Unlock()
	if code, out := e.post(t, phone, e.row("comment13")); code != 200 || out["created"] != true || len(e.rules(t)) != 1 {
		t.Fatalf("make with the audit away: %d %v", code, out)
	}
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+e.rules(t)[0]["id"].(string), phone, ""); code != 200 || len(e.rules(t)) != 0 {
		t.Fatalf("drop with the audit away: %d, %d rules", code, len(e.rules(t)))
	}
}

// hangingWrites is a ledger store whose write of the rules' setting hangs while hang is set: until the call's own end, or
// three seconds, which it answers as "no limit".
type hangingWrites struct {
	*sentLedgerStore
	hang atomic.Bool
}

func (s *hangingWrites) PutSetting(ctx context.Context, key string, v any) error {
	if key == chatRulesKey && s.hang.Load() {
		return hangUntil(ctx)
	}
	return s.sentLedgerStore.PutSetting(ctx, key, v)
}

// Over the routes, a store that hangs is answered within the limit and costs the user nothing they asked for: the make is
// refused (500, nothing in the gate), the drop says the rule is off but still stored, and the routes work again when the
// store does.
func TestChatRuleRoutesAnswerWithinTheLimitWhenTheStoreHangs(t *testing.T) {
	shortStoreTime(t)
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	hanging := &hangingWrites{sentLedgerStore: e.st}
	e.h.store = hanging
	_, made := e.post(t, phone, e.row("comment12"))
	key := made["rule"].(map[string]any)["id"].(string)

	hanging.hang.Store(true)
	for name, req := range map[string]struct {
		method, path, body string
		status             int
		code               string
	}{
		"make": {"POST", chatRulesPath, e.row("comment13"), 500, "timeout"}, // the manager's own answer to a call that ran out of time
		"drop": {"DELETE", chatRulesPath + "/" + key, "", 500, "store_failed"},
	} {
		start := time.Now()
		code, out := call(t, e.mux, req.method, req.path, phone, req.body)
		if d := time.Since(start); d > time.Second || code != req.status || out["code"] != req.code {
			t.Errorf("%s: %d %v after %v; want %d %q within the limit", name, code, out, d, req.status, req.code)
		}
	}
	if len(e.h.connectors.UserRules()) != 0 || !chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Error("the chat of the rule that was dropped was not asked about")
	}
	hanging.hang.Store(false)
	if code, out := call(t, e.mux, "DELETE", chatRulesPath+"/"+key, phone, ""); code != 200 {
		t.Errorf("the drop once the store answers: %d %v", code, out)
	}
	if code, out := e.post(t, phone, e.row("comment13")); code != 200 || out["created"] != true {
		t.Errorf("a make once the store answers: %d %v", code, out)
	}
}

// A rule is dropped by the id of one a gate runs, or finished off when the store still has it: not by the id of whatever
// a hand-edited setting holds. Junk has an id of its own (an entry that is no one-chat rule has the empty chat, whose key
// anyone can work out), and a drop of it must not take every other entry of that kind with it, nor an entry that names a
// chat but is not an allow.
func TestDroppingByIdReachesOnlyRulesAGateCouldRun(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	confirm13 := ghUserRule(13)
	confirm13.Rule.Decision = connector.Confirm
	wildcard := connector.UserRule{Rule: connector.Rule{Connector: "*", Action: "*", Decision: connector.Allow}}
	other := connector.UserRule{Rule: connector.Rule{Connector: "github", Action: "github_review", Decision: connector.Allow}}
	if err := e.st.PutSetting(ctx, chatRulesKey, []connector.UserRule{wildcard, confirm13, other, ghUserRule(12)}); err != nil {
		t.Fatal(err)
	}
	if err := e.h.loadUserRules(ctx); err != nil {
		t.Fatal(err)
	}
	held := func() int {
		var kept []connector.UserRule
		if _, err := e.st.Setting(ctx, chatRulesKey, &kept); err != nil {
			t.Fatal(err)
		}
		return len(kept)
	}
	for name, u := range map[string]connector.UserRule{"the empty chat": {}, "a wildcard rule": wildcard, "another that is none": other, "a confirm for a chat": confirm13} {
		if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+chatRuleKey(u), phone, ""); code != 404 || held() != 4 {
			t.Errorf("%s: dropped by its id: %d, the setting holds %d entries", name, code, held())
		}
	}
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+chatRuleKey(ghUserRule(12)), phone, ""); code != 200 || held() != 3 {
		t.Errorf("the rule that is one: %d, the setting holds %d entries", code, held())
	}
}

// A hand-edited setting with slips (a number, a rule that is not an object, a null) loses only the slips: the rules around
// them are kept, listed and in force, and the next make writes the list without them, the good rules still in it.
func TestChatRuleRoutesWithAHandEditedSettingThatSlipped(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	phone := deviceToken("dev_phone", roleFull)
	good12, _ := json.Marshal(ghUserRule(12))
	if err := e.st.PutSetting(ctx, chatRulesKey, []json.RawMessage{json.RawMessage(`5`), good12, json.RawMessage(`{"rule": 7}`), json.RawMessage(`null`)}); err != nil {
		t.Fatal(err)
	}
	e.h.userRules.loaded = false // as after a restart
	e.h.userRules.snap.Store(nil)
	if err := e.h.loadUserRules(ctx); err != nil {
		t.Fatal(err)
	}
	if list := e.rules(t); len(list) != 1 || list[0]["chat"] != "example-org/example-repo#12" || len(e.h.connectors.UserRules()) != 1 {
		t.Fatalf("listed %v, the gate has %+v", list, e.h.connectors.UserRules())
	}
	if chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) {
		t.Error("the good rule is not in force")
	}
	if code, out := e.post(t, phone, e.row("comment13")); code != 200 || out["created"] != true {
		t.Fatalf("make: %d %v", code, out)
	}
	var kept []connector.UserRule
	if _, err := e.st.Setting(ctx, chatRulesKey, &kept); err != nil || !slices.Equal(idsOf(kept), []string{"github:example-org/example-repo#12", "github:example-org/example-repo#13"}) {
		t.Fatalf("the setting after the make: %v, %v", idsOf(kept), err)
	}
	if list := e.rules(t); len(list) != 2 || chatWrite(t, e.h, e.gh, "github_comment", chatIssue12) || chatWrite(t, e.h, e.gh, "github_comment", chatIssue13) {
		t.Errorf("after the make: %v", list)
	}
}

func TestClubRuleRoutesAndAuthRedLines(t *testing.T) {
	ctx := context.Background()
	e := chatRoutes(t)
	club := &chatClub{}
	if err := e.h.addConnector(club); err != nil {
		t.Fatal(err)
	}
	clubSendID, err := e.st.RecordSentAsUser(ctx, db.SentAsUser{
		Connector:  "club",
		Action:     "club_agent_send",
		Target:     json.RawMessage(`{"session_key":"agent:cue:acc_route:sess_route"}`),
		Origin:     json.RawMessage(`{}`),
		LetThrough: json.RawMessage(`{"kind":"card"}`),
		Summary:    "club_agent_send session_key agent:cue:acc_route:sess_route",
	})
	if err != nil {
		t.Fatal(err)
	}
	clubStartID, err := e.st.RecordSentAsUser(ctx, db.SentAsUser{
		Connector:  "club",
		Action:     "club_agent_start",
		Target:     json.RawMessage(`{"account_id":"acc_start"}`),
		Origin:     json.RawMessage(`{}`),
		LetThrough: json.RawMessage(`{"kind":"card"}`),
		Summary:    "club_agent_start account_id acc_start",
	})
	if err != nil {
		t.Fatal(err)
	}

	phone := deviceToken("dev_phone", roleFull)
	watch := deviceToken("dev_watch", roleAnswer)
	pairing := "pair_" + randomHex(4)
	testDevices.Store(pairing, principal{ID: "pair", Role: roleFull, Paired: true})
	ext := e.h.extTokens.issueAll("example-club-caller", "Club Caller")

	// 1. Auth red lines on add (POST) and update (PATCH/PUT) with TTL, max_uses, session_key and account_id:
	// Every caller without byOwnDevice (master token, answer-only device, pairing token, extension token, unknown token)
	// must be refused.
	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"owner master token":    {e.master(), 403},
		"answer-only device":    {watch, 403},
		"pairing token":         {pairing, 403},
		"extension token":       {ext, 403},
		"unauthenticated token": {"invalid_tok", 401},
	} {
		for _, body := range []string{
			fmt.Sprintf(`{"row":%d,"ttl_seconds":300,"max_uses":5}`, clubSendID),
			fmt.Sprintf(`{"row":%d,"scope":"account","ttl_seconds":600}`, clubSendID),
			`{"session_key":"agent:cue:acc_1:sess_1","ttl_seconds":300,"max_uses":5}`,
			`{"account_id":"acc_1","ttl_seconds":600}`,
		} {
			if code, _ := e.post(t, tc.token, body); code != tc.want {
				t.Errorf("%s POST %s: got %d, want %d", name, body, code, tc.want)
			}
		}
		for _, method := range []string{"PATCH", "PUT"} {
			if code, _ := call(t, e.mux, method, chatRulesPath+"/cr_0000000000000000", tc.token, `{"ttl_seconds":600,"max_uses":10}`); code != tc.want {
				t.Errorf("%s %s: got %d, want %d", name, method, code, tc.want)
			}
		}
	}
	// Delete (DELETE) also refuses non-authorized callers (answer-only, pairing, extension, unknown).
	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"answer-only device":    {watch, 403},
		"pairing token":         {pairing, 403},
		"extension token":       {ext, 403},
		"unauthenticated token": {"invalid_tok", 401},
	} {
		if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/cr_0000000000000000", tc.token, ""); code != tc.want {
			t.Errorf("%s DELETE: got %d, want %d", name, code, tc.want)
		}
	}
	if len(e.rules(t)) != 0 || len(e.h.connectors.UserRules()) != 0 {
		t.Fatalf("rejected auth calls modified rules: %v", e.rules(t))
	}

	// 2. Authorized own-device (byOwnDevice) creates club session rule from row with ttl_seconds and max_uses.
	code, out := e.post(t, phone, fmt.Sprintf(`{"row":%d,"ttl_seconds":600,"max_uses":3}`, clubSendID))
	if code != 200 || out["created"] != true {
		t.Fatalf("create club session rule from row: %d %v", code, out)
	}
	ruleMap := out["rule"].(map[string]any)
	sessRuleKey := ruleMap["id"].(string)
	if ruleMap["connector"] != "club" || ruleMap["chat"] != "session:agent:cue:acc_route:sess_route" ||
		ruleMap["max_uses"] != float64(3) || ruleMap["expires_at"] == nil {
		t.Fatalf("session rule view mismatch: %v", ruleMap)
	}

	// 3. Authorized own-device updates TTL and max_uses via PATCH /api/v1/connectors/sent/chat-rules/{id}.
	code, out = call(t, e.mux, "PATCH", chatRulesPath+"/"+sessRuleKey, phone, `{"ttl_seconds":1200,"max_uses":7}`)
	if code != 200 || out["rule"].(map[string]any)["max_uses"] != float64(7) {
		t.Fatalf("PATCH club session rule: %d %v", code, out)
	}

	// 4. Authorized own-device creates club account rule from club_agent_start row and via direct account_id.
	code, out = e.post(t, phone, fmt.Sprintf(`{"row":%d}`, clubStartID))
	if code != 200 || out["created"] != true || out["rule"].(map[string]any)["chat"] != "account:acc_start" {
		t.Fatalf("create club account rule from start row: %d %v", code, out)
	}
	code, out = e.post(t, phone, `{"account_id":"acc_direct","ttl_seconds":900,"max_uses":4}`)
	if code != 200 || out["created"] != true || out["rule"].(map[string]any)["chat"] != "account:acc_direct" {
		t.Fatalf("create direct club account rule: %d %v", code, out)
	}
	accDirectKey := out["rule"].(map[string]any)["id"].(string)

	// 5. Invalid inputs (wildcards, negative max_uses, negative ttl_seconds) are rejected with 400.
	for _, badBody := range []string{
		`{"session_key":"agent:cue:*:sess_1"}`,
		`{"account_id":"acc_*"}`,
		`{"account_id":"acc:colon"}`,
		`{"account_id":"acc_direct","max_uses":0}`,
		`{"account_id":"acc_direct","ttl_seconds":0}`,
	} {
		if code, _ := e.post(t, phone, badBody); code != 400 {
			t.Errorf("POST %s: expected 400, got %d", badBody, code)
		}
	}

	// 6. Delete via DELETE /api/v1/connectors/sent/chat-rules/{id}.
	if code, _ := call(t, e.mux, "DELETE", chatRulesPath+"/"+accDirectKey, phone, ""); code != 200 {
		t.Fatalf("DELETE club account rule: %d", code)
	}
}
