package connector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const (
	larkSend   = "POST /conversations/oc_1/messages"
	larkTarget = `{"method":"POST","path":"/conversations/oc_1/messages","command":"ex","conversation":"oc_1"}`
	ghTarget   = `{"ref":"example-org/example-repo#12"}`
)

func ghRule(repo string, number any) Rule {
	return Rule{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": repo, "number": number}, Decision: Allow}
}

// A ledger row gives the narrowest rule for its chat: Lark's the text messages of that conversation, by its id and no
// pattern; GitHub's the comments on that issue or pull request, by repository and number. Rows that cannot name
// exactly one chat give a reason, never a wider rule.
func TestChatRuleFor(t *testing.T) {
	larkRule := func(conv string) *Rule {
		return &Rule{Connector: "lark", Action: "POST /conversations/" + conv + "/messages", Decision: Allow}
	}
	for _, c := range []struct {
		name, connector, action, target string
		want                            *Rule // nil: no rule, with the code below
		code                            string
	}{
		{"a Lark text message", "lark", larkSend, larkTarget, larkRule("oc_1"), ""},
		{"an image: the text rule of its chat", "lark", "POST /conversations/oc_2/images", `{"conversation":"oc_2"}`, larkRule("oc_2"), ""},
		{"a file", "lark", "POST /conversations/oc_2/files", `{"conversation":"oc_2"}`, larkRule("oc_2"), ""},
		{"an attachment", "lark", "POST /conversations/oc_2/attachments", `{"conversation":"oc_2"}`, larkRule("oc_2"), ""},
		{"an id of the API's own form", "lark", "POST /conversations/x:12.3-4@y/messages", `{"conversation":"x:12.3-4@y"}`, larkRule("x:12.3-4@y"), ""},

		{"a reply names no conversation", "lark", "POST /messages/om_1/replies", `{"message":"om_1"}`, nil, NoChatReply},
		{"a reply in a conversation's path", "lark", "POST /conversations/oc_1/messages/om_1/replies", `{"conversation":"oc_1","message":"om_1"}`, nil, NoChatReply},
		{"a reply route directly under the conversation", "lark", "POST /conversations/oc_1/replies", `{"conversation":"oc_1"}`, nil, NoChatReply},
		{"a reaction", "lark", "POST /messages/om_1/reactions", `{"message":"om_1"}`, nil, NoChatRoute},
		{"an edit", "lark", "PUT /conversations/oc_1/messages", `{"conversation":"oc_1"}`, nil, NoChatRoute},
		{"a read position", "lark", "POST /conversations/oc_1/read-position", `{"conversation":"oc_1"}`, nil, NoChatRoute},
		{"a deeper path", "lark", "POST /conversations/oc_1/messages/om_1", `{"conversation":"oc_1","message":"om_1"}`, nil, NoChatRoute},
		{"a trailing slash", "lark", "POST /conversations/oc_1/messages/", `{"conversation":"oc_1"}`, nil, NoChatRoute},
		{"a method alone", "lark", "POST", `{}`, nil, NoChatRoute},
		{"no method", "lark", "/conversations/oc_1/messages", `{"conversation":"oc_1"}`, nil, NoChatRoute},
		{"a lowercase method", "lark", "post /conversations/oc_1/messages", `{"conversation":"oc_1"}`, nil, NoChatRoute},
		{"the row names no conversation", "lark", larkSend, `{"method":"POST"}`, nil, NoChatTarget},
		{"a target that is not an object", "lark", larkSend, `"oc_1"`, nil, NoChatTarget},
		{"no target", "lark", larkSend, ``, nil, NoChatTarget},
		{"the target names another conversation", "lark", larkSend, `{"conversation":"oc_2"}`, nil, NoChatTarget},
		{"a number for an id", "lark", larkSend, `{"conversation":1}`, nil, NoChatTarget},
		{"a star for an id", "lark", "POST /conversations/*/messages", `{"conversation":"*"}`, nil, NoChatTarget},
		{"two stars", "lark", "POST /conversations/**/messages", `{"conversation":"**"}`, nil, NoChatTarget},
		{"a pattern in an id", "lark", "POST /conversations/oc_*/messages", `{"conversation":"oc_*"}`, nil, NoChatTarget},
		{"a bracket", "lark", "POST /conversations/oc[1]/messages", `{"conversation":"oc[1]"}`, nil, NoChatTarget},
		{"a question mark", "lark", "POST /conversations/oc?/messages", `{"conversation":"oc?"}`, nil, NoChatTarget},
		{"a backslash", "lark", `POST /conversations/oc\1/messages`, `{"conversation":"oc\\1"}`, nil, NoChatTarget},
		{"a dot segment", "lark", "POST /conversations/../messages", `{"conversation":".."}`, nil, NoChatTarget},
		{"an empty id", "lark", "POST /conversations//messages", `{"conversation":""}`, nil, NoChatTarget},
		{"a space in an id", "lark", "POST /conversations/a b/messages", `{"conversation":"a b"}`, nil, NoChatTarget},
		{"an id of 201 characters", "lark", "POST /conversations/" + strings.Repeat("a", 201) + "/messages", `{"conversation":"` + strings.Repeat("a", 201) + `"}`, nil, NoChatTarget},

		{"a GitHub comment", "github", "github_comment", ghTarget, &Rule{Connector: "github", Action: "github_comment",
			Match: map[string]any{"repo": "example-org/example-repo", "number": float64(12)}, Decision: Allow}, ""},
		{"a GitHub review is not a comment", "github", "github_review", `{"ref":"example-org/example-repo#12","event":"APPROVE"}`, nil, NoChatReview},
		{"another GitHub action", "github", "github_merge", ghTarget, nil, NoChatRoute},
		{"no ref", "github", "github_comment", `{}`, nil, NoChatTarget},
		{"a ref without a number", "github", "github_comment", `{"ref":"example-org/example-repo"}`, nil, NoChatTarget},
		{"number 0", "github", "github_comment", `{"ref":"example-org/example-repo#0"}`, nil, NoChatTarget},
		{"a negative number", "github", "github_comment", `{"ref":"example-org/example-repo#-3"}`, nil, NoChatTarget},
		{"a leading zero", "github", "github_comment", `{"ref":"example-org/example-repo#012"}`, nil, NoChatTarget},
		{"a ten-digit number", "github", "github_comment", `{"ref":"example-org/example-repo#1234567890"}`, nil, NoChatTarget},
		{"a pattern in the repository", "github", "github_comment", `{"ref":"example-org/*#1"}`, nil, NoChatTarget},
		{"a repository that changes the path", "github", "github_comment", `{"ref":"../x#1"}`, nil, NoChatTarget},
		{"an owner only", "github", "github_comment", `{"ref":"example-org#1"}`, nil, NoChatTarget},
		{"a ref with a tail", "github", "github_comment", `{"ref":"example-org/example-repo#12 "}`, nil, NoChatTarget},

		{"mail", "mail", "mail_send", `{"to":["a@example.com"],"subject":"hi"}`, nil, NoChatMail},
		{"mail by its action alone", "", "mail_send", `{}`, nil, NoChatMail},
		{"an extension's own record (Lark)", "lark", "sent.record", `{"conversation":"oc_1"}`, nil, NoChatRecorded},
		{"an extension's own record (GitHub's id)", "github", "sent.record", ghTarget, nil, NoChatRecorded},
		{"an extension's own record", "chatapp", "sent.record", `{"chat":"u1"}`, nil, NoChatRecorded},
		{"another connector", "chatapp", "chatapp_send", `{"chat":"u1"}`, nil, NoChatUnknown},
		{"a wildcard connector", "*", "*", `{}`, nil, NoChatUnknown},
		{"nothing at all", "", "", ``, nil, NoChatUnknown},
	} {
		got, err := ChatRuleFor(c.connector, c.action, json.RawMessage(c.target))
		if c.want == nil {
			var no *NoChatRuleError
			if !errors.As(err, &no) || no.Code != c.code || no.Reason == "" || got.Rule.Connector != "" {
				t.Errorf("%s: %+v, %v; want no rule, code %q", c.name, got, err, c.code)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got.Rule, *c.want) || got.Validate() != nil || got.ID() == "" {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, *c.want)
		}
	}
	// The ID is the chat: the same for rows of the same chat, however they went there.
	a, _ := ChatRuleFor("lark", larkSend, json.RawMessage(larkTarget))
	b, _ := ChatRuleFor("lark", "POST /conversations/oc_1/images", json.RawMessage(`{"conversation":"oc_1"}`))
	g, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	if a.ID() != "lark:oc_1" || a.ID() != b.ID() || g.ID() != "github:example-org/example-repo#12" {
		t.Errorf("ids %q %q %q", a.ID(), b.ID(), g.ID())
	}
}

// narrow checks what every rule ChatRuleFor makes has: it allows, names its connector and one action, pins one chat
// with no pattern anywhere and no empty match, and survives being kept as JSON.
func narrow(t *testing.T, connectorID, action, target string, u UserRule) {
	t.Helper()
	r := u.Rule
	if r.Decision != Allow || r.Connector != connectorID || strings.ContainsAny(r.Connector+r.Action, "*?[\\") || r.Action == "" || r.Action == "*" {
		t.Errorf("%s %s %s: a broader rule than one chat: %+v", connectorID, action, target, r)
	}
	if connectorID == "github" {
		repo, _ := r.Match["repo"].(string)
		number, whole := wholeNumber(r.Match["number"])
		if len(r.Match) != 2 || !githubRepo.MatchString(repo) || strings.ContainsAny(repo, "*?[\\") || !whole || number < 1 || r.Action != "github_comment" {
			t.Errorf("%s %s %s: GitHub rule with a loose match: %+v", connectorID, action, target, r)
		}
		if _, isFloat := r.Match["number"].(float64); !isFloat {
			t.Errorf("%s %s %s: a number the gate's matching would never equal: %#v", connectorID, action, target, r.Match["number"])
		}
	}
	if connectorID == "lark" {
		segs := strings.Split(r.Action, "/")
		if len(r.Match) != 0 || len(segs) != 4 || segs[0] != "POST " || segs[1] != "conversations" || segs[3] != "messages" ||
			strings.Trim(segs[2], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:@=+-") != "" || segs[2] == "" || segs[2] == "." || segs[2] == ".." {
			t.Errorf("%s %s %s: Lark rule that is not one conversation's messages: %+v", connectorID, action, target, r)
		}
	}
	if connectorID == "club" {
		if len(r.Match) != 1 {
			t.Errorf("%s %s %s: club rule must match exactly one field: %+v", connectorID, action, target, r)
		}
		if sk, ok := r.Match["session_key"].(string); ok {
			if r.Action != "club_agent_send" || !clubSessionKeyOK(sk) || strings.ContainsAny(sk, "*?[\\") {
				t.Errorf("%s %s %s: club session rule with loose session_key: %+v", connectorID, action, target, r)
			}
		} else if acc, ok := r.Match["account_id"].(string); ok {
			if r.Action != "club_agent_start" || !clubAccountIDOK(acc) || strings.ContainsAny(acc, "*?[\\:") {
				t.Errorf("%s %s %s: club account rule with loose account_id: %+v", connectorID, action, target, r)
			}
		} else {
			t.Errorf("%s %s %s: club rule with unknown match fields: %+v", connectorID, action, target, r)
		}
	}
	if err := u.Validate(); err != nil {
		t.Errorf("%s %s %s: %v", connectorID, action, target, err)
	}
	raw, _ := json.Marshal(u)
	var back UserRule
	if err := json.Unmarshal(raw, &back); err != nil || back.Validate() != nil || back.ID() != u.ID() {
		t.Errorf("%s %s %s: kept as %s, read back %+v, %v", connectorID, action, target, raw, back, err)
	}
}

func FuzzChatRuleForIsNeverBroader(f *testing.F) {
	for _, s := range [][3]string{
		{"lark", larkSend, larkTarget}, {"lark", "POST /conversations/*/messages", `{"conversation":"*"}`},
		{"lark", "POST /conversations/oc_1/messages/om_1/replies", `{"conversation":"oc_1"}`}, {"github", "github_comment", ghTarget},
		{"github", "github_comment", `{"ref":"a/*#1"}`}, {"github", "github_review", ghTarget}, {"mail", "mail_send", `{}`}, {"*", "*", `{}`},
		{"club", "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1"}`},
		{"club", "club_agent_interrupt", `{"session_key":"agent:cue:*:sess_1"}`},
		{"club", "club_agent_start", `{"account_id":"acc_1"}`},
		{"club", "club_agent_start", `{"account_id":"acc_*"}`},
	} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, connectorID, action, target string) {
		if u, err := ChatRuleFor(connectorID, action, json.RawMessage(target)); err == nil {
			narrow(t, connectorID, action, target, u)
		} else if no := (*NoChatRuleError)(nil); !errors.As(err, &no) || no.Reason == "" {
			t.Errorf("%s %s %s: %v is not a reason", connectorID, action, target, err)
		}
	})
}

// A rule is a user's only when it is exactly one chat's allow: every other decision, a pattern for the chat, a loose
// or extra match, another action or route is refused, so a stored or edited rule cannot widen what the user chose.
func TestUserRuleValidate(t *testing.T) {
	lark := func(action string, m map[string]any) Rule {
		return Rule{Connector: "lark", Action: action, Match: m, Decision: Allow}
	}
	for _, c := range []struct {
		name string
		rule Rule
		ok   bool
	}{
		{"Lark messages of one conversation", lark(larkSend, nil), true},
		{"with an empty match map", lark(larkSend, map[string]any{}), true},
		{"GitHub, number as JSON gives it", ghRule("example-org/example-repo", float64(12)), true},
		{"GitHub, number as Go has it", ghRule("example-org/example-repo", 12), true},

		{"confirm", Rule{Connector: "lark", Action: larkSend, Decision: Confirm}, false},
		{"deny", Rule{Connector: "lark", Action: larkSend, Decision: Deny}, false},
		{"no decision", Rule{Connector: "lark", Action: larkSend}, false},
		{"everything", Rule{Connector: "*", Action: "*", Decision: Allow}, false},
		{"every Lark call", lark("*", nil), false},
		{"every conversation", lark("POST /conversations/*/messages", nil), false},
		{"all that follows", lark("POST /conversations/**", nil), false},
		{"a conversation's whole path", lark("POST /conversations/oc_1/**", nil), false},
		{"Lark with a match", lark(larkSend, map[string]any{"text": "hi"}), false},
		{"images", lark("POST /conversations/oc_1/images", nil), false},
		{"a reply", lark("POST /messages/om_1/replies", nil), false},
		{"a reply in a conversation", lark("POST /conversations/oc_1/messages/om_1/replies", nil), false},
		{"another method", lark("PUT /conversations/oc_1/messages", nil), false},
		{"a lowercase method", lark("post /conversations/oc_1/messages", nil), false},
		{"a space after the path", lark(larkSend+" ", nil), false},
		{"a pattern connector", Rule{Connector: "lar*", Action: larkSend, Decision: Allow}, false},
		{"GitHub without a match", Rule{Connector: "github", Action: "github_comment", Decision: Allow}, false},
		{"GitHub with a repository only", Rule{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/example-repo"}, Decision: Allow}, false},
		{"GitHub with a number only", Rule{Connector: "github", Action: "github_comment", Match: map[string]any{"number": float64(12)}, Decision: Allow}, false},
		{"GitHub with a repository pattern", ghRule("example-org/*", float64(12)), false},
		{"GitHub with a number as text", ghRule("example-org/example-repo", "12"), false},
		{"GitHub with a half number", ghRule("example-org/example-repo", 1.5), false},
		{"GitHub with number 0", ghRule("example-org/example-repo", float64(0)), false},
		{"GitHub with a number past the limit", ghRule("example-org/example-repo", float64(1e9)), false},
		{"GitHub with a bad repository", ghRule("../x", float64(1)), false},
		{"GitHub with a body match", Rule{Connector: "github", Action: "github_comment",
			Match: map[string]any{"repo": "example-org/example-repo", "number": float64(12), "body": "*"}, Decision: Allow}, false},
		{"GitHub reviews", Rule{Connector: "github", Action: "github_review",
			Match: map[string]any{"repo": "example-org/example-repo", "number": float64(12)}, Decision: Allow}, false},
		{"every GitHub write", Rule{Connector: "github", Action: "github_*",
			Match: map[string]any{"repo": "example-org/example-repo", "number": float64(12)}, Decision: Allow}, false},
		{"another connector", Rule{Connector: "chatapp", Action: "chatapp_send", Match: map[string]any{"chat": "u1"}, Decision: Allow}, false},
		{"mail", Rule{Connector: "mail", Action: "mail_send", Match: map[string]any{"to": "a@example.com"}, Decision: Allow}, false},
	} {
		err := (UserRule{Rule: c.rule}).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// ghFake is a connector with GitHub's id and its two write actions, with the arguments the gate sees.
func ghFake() (*fake, Connector) {
	f := &fake{id: "github", status: Status{State: StateOK}}
	return f, writer{f}
}

// quiet is a GitHub-like writer whose writes keep no record, so calls from several goroutines share nothing.
type quiet struct{ writer }

func (quiet) Do(context.Context, string, json.RawMessage) (string, error) { return "done", nil }

// outcome is what happened to a write: it ran unasked, it ran after the user said yes, or it was refused (a deny, or
// the user's no).
func outcome(ctx context.Context, r *Registry, tool, args string, answer bool) string {
	asked := 0
	ask := func(context.Context, string, json.RawMessage) bool { asked++; return answer }
	res := r.Call(ctx, tool, json.RawMessage(args), ask)
	switch {
	case res.IsError:
		return "refused"
	case asked > 0:
		return "ran after a card"
	}
	return "ran"
}

const (
	cmt12 = `{"repo":"example-org/example-repo","number":12,"body":"ok"}`
	cmt13 = `{"repo":"example-org/example-repo","number":13,"body":"ok"}`
)

// A chat the user stopped asking about goes through without a card, and every other write still asks: another issue,
// another repository, another action, arguments that do not name the chat the way the rule does. Adding the rule and
// dropping it again take effect on the next write, with no restart.
func TestUserRuleLetsOneChatThrough(t *testing.T) {
	ctx := context.Background()
	_, conn := ghFake()
	r := NewRegistry(nil)
	if err := r.Add(conn); err != nil {
		t.Fatal(err)
	}
	var sent []Sent
	r.SetSent(func(_ context.Context, s Sent) { sent = append(sent, s) })
	rule, err := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	if err != nil {
		t.Fatal(err)
	}
	// Kept and read back as JSON, which is what a store will do: the rule must still match (numbers become float64).
	raw, _ := json.Marshal([]UserRule{rule})
	var kept []UserRule
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatal(err)
	}

	if got := outcome(ctx, r, "github_comment", cmt12, true); got != "ran after a card" {
		t.Fatalf("before the rule: %s", got)
	}
	if err := r.SetUserRules(kept); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, tool, args, want string }{
		{"the chat", "github_comment", cmt12, "ran"},
		{"the chat, other words", "github_comment", `{"repo":"example-org/example-repo","number":12,"body":"something else"}`, "ran"},
		{"another number", "github_comment", cmt13, "ran after a card"},
		{"another repository", "github_comment", `{"repo":"example-org/other","number":12,"body":"ok"}`, "ran after a card"},
		{"another owner", "github_comment", `{"repo":"other-org/example-repo","number":12,"body":"ok"}`, "ran after a card"},
		{"another action on the chat", "github_note", cmt12, "ran after a card"},
		{"the thread page's form", "github_comment", `{"chat":"example-org/example-repo#12","text":"ok"}`, "ran after a card"},
		{"a number as text", "github_comment", `{"repo":"example-org/example-repo","number":"12","body":"ok"}`, "ran after a card"},
		{"no number", "github_comment", `{"repo":"example-org/example-repo","body":"ok"}`, "ran after a card"},
		{"nothing", "github_comment", `{}`, "ran after a card"},
	} {
		if got := outcome(ctx, r, c.tool, c.args, true); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// What the rule let through is in the ledger as a rule, with the rule that did.
	if s := sent[1]; s.Through.Kind != ThroughRule || s.Through.Rule == nil || s.Through.Rule.Match["repo"] != "example-org/example-repo" {
		t.Errorf("let through by %+v", s.Through)
	}
	// A card the user declines still stops everything else.
	if got := outcome(ctx, r, "github_comment", cmt13, false); got != "refused" {
		t.Errorf("a declined card: %s", got)
	}

	// Dropped, it asks again; set again, it does not.
	if err := r.SetUserRules(nil); err != nil {
		t.Fatal(err)
	}
	if got := outcome(ctx, r, "github_comment", cmt12, true); got != "ran after a card" {
		t.Errorf("after dropping the rule: %s", got)
	}
	_ = r.SetUserRules(kept)
	if got := outcome(ctx, r, "github_comment", cmt12, true); got != "ran" {
		t.Errorf("after setting it again: %s", got)
	}
}

// The hand-written rules come first: one that matches the chat and denies or confirms still wins over the user's
// allow, and a hand-written rule for somewhere else changes nothing.
func TestUserRuleComesAfterHandWritten(t *testing.T) {
	rule, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	for _, c := range []struct {
		name string
		hand []Rule
		want string
	}{
		{"none", nil, "ran"},
		{"a deny of the repository", []Rule{{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/*"}, Decision: Deny}}, "refused"},
		{"a deny of every GitHub write", []Rule{{Connector: "github", Action: "*", Decision: Deny}}, "refused"},
		{"a deny of everything", []Rule{{Connector: "*", Action: "*", Decision: Deny}}, "refused"},
		{"a confirm of the chat", []Rule{{Connector: "github", Action: "github_comment", Match: map[string]any{"number": float64(12)}, Decision: Confirm}}, "ran after a card"},
		{"a confirm of everything", []Rule{{Connector: "*", Action: "*", Decision: Confirm}}, "ran after a card"},
		{"a deny of another repository", []Rule{{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "other-org/*"}, Decision: Deny}}, "ran"},
		{"a hand-written allow of the same chat", []Rule{{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/*"}, Decision: Allow}}, "ran"},
	} {
		_, conn := ghFake()
		r := NewRegistry(c.hand)
		_ = r.Add(conn)
		if err := r.SetUserRules([]UserRule{rule}); err != nil {
			t.Fatal(err)
		}
		if got := outcome(context.Background(), r, "github_comment", cmt12, true); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// The order is the contract, not just the outcome: hand-written rules, then the user's.
	r := NewRegistry([]Rule{{Connector: "x", Action: "x_a", Decision: Deny}})
	_ = r.SetUserRules([]UserRule{rule})
	if got := r.gateRules(); len(got) != 2 || got[0].Connector != "x" || got[1].Connector != "github" {
		t.Errorf("order %+v", got)
	}
}

// An allow of the user's is clamped like any allow: a connector's confirm_always action and a health conversation's
// write still ask, with the user's rule there.
func TestUserRuleIsClampedLikeAnyAllow(t *testing.T) {
	rule, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	f := &fake{id: "github", status: Status{State: StateOK}}
	r := NewRegistry(nil)
	_ = r.Add(guarded{f}) // github_comment is in its confirm_always
	_ = r.SetUserRules([]UserRule{rule})
	if got := outcome(context.Background(), r, "github_comment", cmt12, true); got != "ran after a card" {
		t.Errorf("confirm_always: %s", got)
	}
	if got := outcome(context.Background(), r, "github_comment", cmt12, false); got != "refused" || len(f.did) != 1 {
		t.Errorf("confirm_always, declined: %s, did %v", got, f.did)
	}

	_, conn := ghFake()
	r = NewRegistry(nil)
	_ = r.Add(conn)
	_ = r.SetUserRules([]UserRule{rule})
	if got := outcome(ConfirmEach(context.Background()), r, "github_comment", cmt12, true); got != "ran after a card" {
		t.Errorf("a health conversation: %s", got)
	}
	if got := outcome(context.Background(), r, "github_comment", cmt12, true); got != "ran" {
		t.Errorf("elsewhere: %s", got)
	}
}

// With no user rule the gate decides exactly as before: the same outcome for the hand-written rules of the other
// tests, whether SetUserRules was never called, was given nothing, or had rules that were dropped again.
func TestNoUserRulesDecideAsBefore(t *testing.T) {
	rule, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	sets := map[string][]Rule{
		"no rules":    nil,
		"allow all":   {{Connector: "*", Action: "*", Decision: Allow}},
		"deny all":    {{Connector: "*", Action: "*", Decision: Deny}},
		"confirm all": {{Connector: "*", Action: "*", Decision: Confirm}},
		"a note denied, one repository allowed": {
			{Connector: "github", Action: "github_note", Decision: Deny},
			{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/*"}, Decision: Allow}},
		"the chat's own shape by hand": {ghRule("example-org/example-repo", float64(12))},
	}
	calls := []struct{ tool, args string }{
		{"github_comment", cmt12}, {"github_comment", cmt13}, {"github_comment", `{"repo":"other/x","number":1}`},
		{"github_note", cmt12}, {"github_comment", `{}`}, {"github_read", `{}`},
	}
	for name, hand := range sets {
		variants := map[string]*Registry{}
		for _, v := range []string{"untouched", "nil", "empty", "set then dropped"} {
			_, conn := ghFake()
			r := NewRegistry(hand)
			_ = r.Add(conn)
			switch v {
			case "nil":
				_ = r.SetUserRules(nil)
			case "empty":
				_ = r.SetUserRules([]UserRule{})
			case "set then dropped":
				_ = r.SetUserRules([]UserRule{rule})
				_ = r.SetUserRules(nil)
			}
			variants[v] = r
		}
		for _, call := range calls {
			for _, answer := range []bool{true, false} {
				want := outcome(context.Background(), variants["untouched"], call.tool, call.args, answer)
				for v, r := range variants {
					if got := outcome(context.Background(), r, call.tool, call.args, answer); got != want {
						t.Errorf("%s, %s %s (card answered %v), %s: %s, untouched says %s", name, call.tool, call.args, answer, v, got, want)
					}
				}
			}
		}
		for v, r := range variants {
			if got := r.gateRules(); !reflect.DeepEqual(got, hand) {
				t.Errorf("%s, %s: the gate's rules are %+v, not %+v", name, v, got, hand)
			}
			if r.UserRules() != nil {
				t.Errorf("%s, %s: user rules %+v", name, v, r.UserRules())
			}
		}
	}
}

// SetUserRules takes only what Validate lets through, leaves the rest out and says which, and still sets the good ones.
// One rule to a chat, and a rule changed afterwards by its owner changes nothing in the gate.
func TestSetUserRulesKeepsOnlyOneChatAllows(t *testing.T) {
	good, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	lark, _ := ChatRuleFor("lark", larkSend, json.RawMessage(larkTarget))
	r := NewRegistry(nil)
	err := r.SetUserRules([]UserRule{
		{Rule: Rule{Connector: "*", Action: "*", Decision: Allow}},
		good,
		{Rule: Rule{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/example-repo", "number": float64(12)}, Decision: Confirm}},
		{Rule: Rule{Connector: "lark", Action: "POST /conversations/*/messages", Decision: Allow}},
		good,
		lark,
	})
	if err == nil || !strings.Contains(err.Error(), "user rule 0") || !strings.Contains(err.Error(), "user rule 2") || !strings.Contains(err.Error(), "user rule 3") ||
		strings.Contains(err.Error(), "user rule 1") || strings.Contains(err.Error(), "user rule 4") {
		t.Fatalf("error %v", err)
	}
	got := r.UserRules()
	if len(got) != 2 || got[0].Connector != "github" || got[1].Connector != "lark" {
		t.Fatalf("set %+v", got)
	}
	good.Rule.Match["repo"] = "example-org/*" // the owner changing its copy later
	if got := r.UserRules(); got[0].Match["repo"] != "example-org/example-repo" {
		t.Fatalf("the gate's rule followed the owner's copy: %+v", got[0])
	}
	if err := r.SetUserRules([]UserRule{lark}); err != nil || len(r.UserRules()) != 1 {
		t.Fatalf("replace: %v %+v", err, r.UserRules())
	}
	var none *Registry
	if none.UserRules() != nil {
		t.Fatal("a nil registry has user rules")
	}
}

// The rules can change while writes are gated (the manager sets them when the user makes or drops one): no call sees a
// half-set list, and under -race nothing races.
func TestUserRulesChangeWhileCalling(t *testing.T) {
	rule, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	r := NewRegistry([]Rule{{Connector: "github", Action: "github_note", Decision: Deny}})
	_ = r.Add(quiet{writer{&fake{id: "github", status: Status{State: StateOK}}}})
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := range 200 {
				if (i+j)%2 == 0 {
					_ = r.SetUserRules([]UserRule{rule})
				} else {
					_ = r.SetUserRules(nil)
				}
				_ = r.UserRules()
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				if got := outcome(context.Background(), r, "github_comment", cmt12, true); got != "ran" && got != "ran after a card" {
					t.Errorf("comment: %s", got)
				}
				if got := outcome(context.Background(), r, "github_note", cmt12, true); got != "refused" {
					t.Errorf("a denied action: %s", got)
				}
			}
		}()
	}
	wg.Wait()
}

// LarkRules is what the node's gate is served: the user's Lark rules as the node reads them ("<METHOD> <path>"), and
// nothing of a GitHub rule, whose action is a tool's name.
func TestUserRulesAsTheNodesGateReadsThem(t *testing.T) {
	lark, _ := ChatRuleFor("lark", larkSend, json.RawMessage(larkTarget))
	gh, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	r := NewRegistry(nil)
	_ = r.SetUserRules([]UserRule{gh, lark})
	got := LarkRules(r.UserRules(), nil)
	if want := []proto.LarkRule{{Method: "POST", Path: "/conversations/oc_1/messages", Action: "allow"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v, want %+v", got, want)
	}
	if got := LarkRules(NewRegistry(nil).UserRules(), nil); got != nil {
		t.Fatalf("no rules: %+v", got)
	}
}

// A rule built in Go with an int (or int64) for the number passes Validate, but the gate compares a Match with the
// arguments as JSON gave them, where a number is a float64: left as it is the rule would never match. It is set the
// way the gate reads it, and is the same rule as one with a float64.
func TestSetUserRulesGivesTheGateAFloatNumber(t *testing.T) {
	for _, number := range []any{12, int64(12), float64(12)} {
		_, conn := ghFake()
		r := NewRegistry(nil)
		_ = r.Add(conn)
		if err := r.SetUserRules([]UserRule{{Rule: ghRule("example-org/example-repo", number)}}); err != nil {
			t.Fatalf("%T: %v", number, err)
		}
		if got, ok := r.UserRules()[0].Match["number"].(float64); !ok || got != 12 {
			t.Errorf("%T: the gate's rule has %#v", number, r.UserRules()[0].Match["number"])
		}
		if got := outcome(context.Background(), r, "github_comment", cmt12, true); got != "ran" {
			t.Errorf("%T: the chat of the rule: %s", number, got)
		}
		if got := outcome(context.Background(), r, "github_comment", cmt13, true); got != "ran after a card" {
			t.Errorf("%T: another issue: %s", number, got)
		}
	}
	// The same chat in two forms is one rule.
	r := NewRegistry(nil)
	if err := r.SetUserRules([]UserRule{{Rule: ghRule("example-org/example-repo", 12)}, {Rule: ghRule("example-org/example-repo", float64(12))}}); err != nil || len(r.UserRules()) != 1 {
		t.Errorf("two forms of one chat: %v, %+v", err, r.UserRules())
	}
	// The owner's copy is not changed.
	own := UserRule{Rule: ghRule("example-org/example-repo", 12)}
	_ = r.SetUserRules([]UserRule{own})
	if _, still := own.Rule.Match["number"].(int); !still {
		t.Errorf("the caller's rule was changed: %#v", own.Rule.Match["number"])
	}
}

// A write a user's rule let through is told apart in what Sent says (the ledger's let_through keeps kind "rule" for it, as
// for a hand-written rule, and adds a mark): so is not one a hand-written allow or a card let through, even when the
// hand-written rule is for the very same chat.
func TestSentSaysWhenAUserRuleLetItThrough(t *testing.T) {
	user, _ := ChatRuleFor("github", "github_comment", json.RawMessage(ghTarget))
	handForRepo := Rule{Connector: "github", Action: "github_comment", Match: map[string]any{"repo": "example-org/hand"}, Decision: Allow}
	_, conn := ghFake()
	r := NewRegistry([]Rule{handForRepo})
	_ = r.Add(conn)
	var got []Sent
	r.SetSent(func(_ context.Context, s Sent) { got = append(got, s) })
	_ = r.SetUserRules([]UserRule{user})
	ask := func(context.Context, string, json.RawMessage) bool { return true }
	call := func(args string) Sent {
		t.Helper()
		got = nil
		if res := r.Call(context.Background(), "github_comment", json.RawMessage(args), ask); res.IsError || len(got) != 1 {
			t.Fatalf("%s: %+v, told %d", args, res, len(got))
		}
		return got[0]
	}
	if s := call(cmt12); s.Through.Kind != ThroughRule || !s.Through.UserRule || s.Through.Rule == nil || s.Through.Rule.Match["repo"] != "example-org/example-repo" {
		t.Errorf("the user's rule: %+v", s.Through)
	}
	if s := call(`{"repo":"example-org/hand","number":3,"body":"x"}`); s.Through.Kind != ThroughRule || s.Through.UserRule {
		t.Errorf("a hand-written rule: %+v", s.Through)
	}
	if s := call(cmt13); s.Through.Kind != ThroughCard || s.Through.UserRule {
		t.Errorf("a card: %+v", s.Through)
	}
	// The mark is not in the rule's JSON, which a hand-written file could say too.
	raw, _ := json.Marshal(call(cmt12).Through.Rule)
	if strings.Contains(string(raw), "user") || strings.Contains(string(raw), "from") {
		t.Errorf("the rule as JSON: %s", raw)
	}
	var fromFile Rule
	if err := json.Unmarshal([]byte(`{"connector":"github","action":"github_comment","decision":"allow","fromUser":true,"user_rule":true}`), &fromFile); err != nil || fromFile.fromUser {
		t.Errorf("a rule of a file cannot say it is the user's: %+v, %v", fromFile, err)
	}
}

type clubTestConn struct {
	*fake
	confirmAlways []string
}

func (c clubTestConn) Manifest() Manifest {
	m := c.fake.Manifest()
	m.ConfirmAlways = c.confirmAlways
	return m
}

func (c clubTestConn) Actions() []proto.Tool {
	return []proto.Tool{
		{Name: "club_agent_send"},
		{Name: "club_agent_interrupt"},
		{Name: "club_agent_start"},
	}
}

func (c clubTestConn) Describe(_ context.Context, name string, args json.RawMessage) (Action, string, error) {
	return Action{Name: name, Visible: "Personal Agent"}, name + " " + string(args), nil
}

func (c clubTestConn) Do(_ context.Context, name string, args json.RawMessage) (string, error) {
	return "done", nil
}

func TestClubChatRuleForAndValidation(t *testing.T) {
	sendRule, err := ChatRuleFor("club", "club_agent_send", json.RawMessage(`{"session_key":"agent:cue:a0123456789ab:sess_1"}`))
	if err != nil || sendRule.ID() != "club:session:agent:cue:a0123456789ab:sess_1" || sendRule.Chat() != "session:agent:cue:a0123456789ab:sess_1" || sendRule.Validate() != nil {
		t.Fatalf("club_agent_send rule: %+v, %v", sendRule, err)
	}
	intRule, err := ChatRuleFor("club", "club_agent_interrupt", json.RawMessage(`{"session_key":"agent:cue:a0123456789ab:sess_1"}`))
	if err != nil || intRule.ID() != sendRule.ID() || !reflect.DeepEqual(intRule.Rule, sendRule.Rule) {
		t.Fatalf("club_agent_interrupt rule: %+v, %v", intRule, err)
	}
	startRule, err := ChatRuleFor("club", "club_agent_start", json.RawMessage(`{"account_id":"a0123456789ab"}`))
	if err != nil || startRule.ID() != "club:account:a0123456789ab" || startRule.Chat() != "account:a0123456789ab" || startRule.Validate() != nil {
		t.Fatalf("club_agent_start rule: %+v, %v", startRule, err)
	}
	accFromSend, err := ClubAccountRuleFor("club_agent_send", json.RawMessage(`{"session_key":"agent:cue:a0123456789ab:sess_1"}`))
	if err != nil || accFromSend.ID() != "club:account:a0123456789ab" {
		t.Fatalf("ClubAccountRuleFor send: %+v, %v", accFromSend, err)
	}

	// Strict session_key and account_id charset validation: reject empty, wildcards, whitespace, control characters, slashes, dots, overlong.
	badKeys := []string{
		"", "*", "?", "agent:cue:*:sess_1", "agent:cue:acc?:sess_1", "agent:cue:acc[1]:sess_1", `agent:cue:acc\1:sess_1`,
		"agent:cue:acc 1:sess_1", "agent:cue:acc\t1:sess_1", "agent:cue:acc\n1:sess_1", "agent:cue:acc\x001:sess_1",
		"agent:cue:acc\x1f1:sess_1", "agent:cue:acc\x7f1:sess_1", "agent/cue/acc/sess", ".", "..", "agent:..:acc:sess",
		"agent::acc:sess", "agent:cue:acc:", ":agent:cue:acc:sess", strings.Repeat("a", 201),
	}
	for _, bad := range badKeys {
		target, _ := json.Marshal(map[string]string{"session_key": bad})
		if _, err := ChatRuleFor("club", "club_agent_send", target); err == nil {
			t.Errorf("ChatRuleFor accepted bad session_key %q", bad)
		}
		u := UserRule{Rule: Rule{Connector: "club", Action: "club_agent_send", Match: map[string]any{"session_key": bad}, Decision: Allow}}
		if u.Validate() == nil {
			t.Errorf("Validate accepted bad session_key %q", bad)
		}
	}
	badAccounts := append(slices.Clone(badKeys), "acc:1", "agent:cue:acc:sess")
	for _, bad := range badAccounts {
		target, _ := json.Marshal(map[string]string{"account_id": bad})
		if _, err := ChatRuleFor("club", "club_agent_start", target); err == nil {
			t.Errorf("ChatRuleFor accepted bad account_id %q", bad)
		}
		u := UserRule{Rule: Rule{Connector: "club", Action: "club_agent_start", Match: map[string]any{"account_id": bad}, Decision: Allow}}
		if u.Validate() == nil {
			t.Errorf("Validate accepted bad account_id %q", bad)
		}
	}

	// ClubAccountRuleFor on session tools requires strict 4-segment session_key.
	for _, badSK := range []string{"sess_only", "agent:cue:acc1", "agent:cue:acc1:s1:extra", "other:cue:acc1:s1", "agent::acc1:s1"} {
		target, _ := json.Marshal(map[string]string{"session_key": badSK})
		if _, err := ClubAccountRuleFor("club_agent_send", target); err == nil {
			t.Errorf("ClubAccountRuleFor accepted malformed 4-segment session_key %q", badSK)
		}
	}

	// TTL and max_uses validation.
	t0 := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	validBounded := sendRule
	validBounded.At = t0
	validBounded.ExpiresAt = t0.Add(30 * time.Minute)
	validBounded.MaxUses = 5
	validBounded.UsedCount = 2
	if err := validBounded.Validate(); err != nil {
		t.Fatalf("valid bounded rule failed Validate: %v", err)
	}
	for name, fn := range map[string]func(UserRule) UserRule{
		"negative max_uses":    func(u UserRule) UserRule { u.MaxUses = -1; return u },
		"negative used_count":  func(u UserRule) UserRule { u.UsedCount = -1; return u },
		"expires_at before at": func(u UserRule) UserRule { u.ExpiresAt = t0.Add(-time.Second); return u },
		"expires_at equal at":  func(u UserRule) UserRule { u.ExpiresAt = t0; return u },
	} {
		if err := fn(validBounded).Validate(); err == nil {
			t.Errorf("%s: expected Validate error", name)
		}
	}
}

func TestClubSessionAndAccountGateMatchingAndRedLines(t *testing.T) {
	ctx := context.Background()
	sessRule, err := ClubSessionRule("agent:cue:acc_1:sess_1")
	if err != nil {
		t.Fatal(err)
	}
	accRule, err := ClubAccountRule("acc_2")
	if err != nil {
		t.Fatal(err)
	}

	newReg := func(hand []Rule, confirmAlways []string, user []UserRule) (*Registry, *[]Sent) {
		r := NewRegistry(hand)
		conn := clubTestConn{fake: &fake{id: "club", status: Status{State: StateOK}}, confirmAlways: confirmAlways}
		if err := r.Add(conn); err != nil {
			t.Fatal(err)
		}
		if err := r.SetUserRules(user); err != nil {
			t.Fatal(err)
		}
		var sent []Sent
		r.SetSent(func(_ context.Context, s Sent) { sent = append(sent, s) })
		return r, &sent
	}

	// 1. club:session:<session_key> matching
	r, sent := newReg(nil, nil, []UserRule{sessRule})
	for _, tc := range []struct {
		name, tool, args, want string
	}{
		{"matching send", "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1","text":"hi"}`, "ran"},
		{"matching interrupt", "club_agent_interrupt", `{"session_key":"agent:cue:acc_1:sess_1","then":"stop"}`, "ran"},
		{"other session", "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_2","text":"hi"}`, "ran after a card"},
		{"start not covered by session rule", "club_agent_start", `{"account_id":"acc_1","session_key":"agent:cue:acc_1:sess_1","text":"hi"}`, "ran after a card"},
		{"missing session_key", "club_agent_send", `{"text":"hi"}`, "ran after a card"},
		{"non-string session_key", "club_agent_send", `{"session_key":123,"text":"hi"}`, "ran after a card"},
		{"case-variant SESSION_KEY", "club_agent_send", `{"SESSION_KEY":"agent:cue:acc_1:sess_1","text":"hi"}`, "ran after a card"},
		{"conflicting account_id", "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1","account_id":"other","text":"hi"}`, "ran after a card"},
	} {
		if got := outcome(ctx, r, tc.tool, tc.args, true); got != tc.want {
			t.Errorf("session rule / %s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if len(*sent) < 2 || (*sent)[0].Through.Kind != ThroughRule || !(*sent)[0].Through.UserRule || (*sent)[1].Through.Kind != ThroughRule || !(*sent)[1].Through.UserRule {
		t.Fatalf("session rule ledger audit mismatch: %+v", *sent)
	}

	// 2. club:account:<account_id> matching & strict 4-segment session_key fail-closed validation
	r, sent = newReg(nil, nil, []UserRule{accRule})
	for _, tc := range []struct {
		name, tool, args, want string
	}{
		{"start matching account_id", "club_agent_start", `{"account_id":"acc_2","text":"start"}`, "ran"},
		{"send matching 4-segment account_id", "club_agent_send", `{"session_key":"agent:cue:acc_2:sess_9","text":"go"}`, "ran"},
		{"interrupt matching 4-segment account_id", "club_agent_interrupt", `{"session_key":"agent:grok_bot:acc_2:sess_9"}`, "ran"},
		{"start other account_id", "club_agent_start", `{"account_id":"acc_1","text":"start"}`, "ran after a card"},
		{"start missing account_id", "club_agent_start", `{"text":"start"}`, "ran after a card"},
		{"start with conflicting session_key", "club_agent_start", `{"account_id":"acc_2","session_key":"agent:cue:acc_1:s1","text":"start"}`, "ran after a card"},
		{"send other account in 3rd segment", "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_9","text":"go"}`, "ran after a card"},
		{"send 1-segment session_key fail-closed", "club_agent_send", `{"session_key":"acc_2","text":"go"}`, "ran after a card"},
		{"send 3-segment session_key fail-closed", "club_agent_send", `{"session_key":"agent:cue:acc_2","text":"go"}`, "ran after a card"},
		{"send 5-segment session_key fail-closed", "club_agent_send", `{"session_key":"agent:cue:acc_2:sess_9:extra","text":"go"}`, "ran after a card"},
		{"send wrong prefix fail-closed", "club_agent_send", `{"session_key":"bot:cue:acc_2:sess_9","text":"go"}`, "ran after a card"},
		{"send empty provider fail-closed", "club_agent_send", `{"session_key":"agent::acc_2:sess_9","text":"go"}`, "ran after a card"},
		{"send empty session fail-closed", "club_agent_send", `{"session_key":"agent:cue:acc_2:","text":"go"}`, "ran after a card"},
		{"send wildcard in session fail-closed", "club_agent_send", `{"session_key":"agent:cue:acc_2:*","text":"go"}`, "ran after a card"},
		{"send conflicting explicit account_id fail-closed", "club_agent_send", `{"session_key":"agent:cue:acc_2:sess_9","account_id":"acc_1","text":"go"}`, "ran after a card"},
	} {
		if got := outcome(ctx, r, tc.tool, tc.args, true); got != tc.want {
			t.Errorf("account rule / %s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	for i := range 3 {
		if (*sent)[i].Through.Kind != ThroughRule || !(*sent)[i].Through.UserRule {
			t.Errorf("account rule ledger entry %d missing ThroughRule/UserRule: %+v", i, (*sent)[i].Through)
		}
	}

	// 3. Red line: Hand-written deny and confirm rules always override UserRule (both session and account).
	for _, tc := range []struct {
		name string
		hand []Rule
		tool string
		args string
		want string
	}{
		{
			"hand-written deny all club",
			[]Rule{{Connector: "club", Action: "*", Decision: Deny}},
			"club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1","text":"hi"}`, "refused",
		},
		{
			"hand-written confirm all club",
			[]Rule{{Connector: "club", Action: "*", Decision: Confirm}},
			"club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1","text":"hi"}`, "ran after a card",
		},
		{
			"hand-written deny specific account overrides account bypass on start and send",
			[]Rule{{Connector: "club", Action: "*", Match: map[string]any{"account_id": "acc_2"}, Decision: Deny}},
			"club_agent_send", `{"session_key":"agent:cue:acc_2:sess_9","text":"hi"}`, "refused",
		},
		{
			"hand-written confirm interrupt overrides account bypass",
			[]Rule{{Connector: "club", Action: "club_agent_interrupt", Decision: Confirm}},
			"club_agent_interrupt", `{"session_key":"agent:cue:acc_2:sess_9"}`, "ran after a card",
		},
	} {
		r, _ := newReg(tc.hand, nil, []UserRule{sessRule, accRule})
		if got := outcome(ctx, r, tc.tool, tc.args, true); got != tc.want {
			t.Errorf("hand-written priority / %s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	// 4. Red line: ConfirmAlways and ConfirmEach clamp Allow back to Confirm.
	rAlwaysConfirm, _ := newReg(nil, []string{"club_agent_*"}, []UserRule{sessRule, accRule})
	if got := outcome(ctx, rAlwaysConfirm, "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1","text":"hi"}`, true); got != "ran after a card" {
		t.Errorf("ConfirmAlways on session rule: got %q, want ran after a card", got)
	}
	if got := outcome(ctx, rAlwaysConfirm, "club_agent_start", `{"account_id":"acc_2","text":"hi"}`, true); got != "ran after a card" {
		t.Errorf("ConfirmAlways on account rule: got %q, want ran after a card", got)
	}
	rNormal, _ := newReg(nil, nil, []UserRule{sessRule, accRule})
	if got := outcome(ConfirmEach(ctx), rNormal, "club_agent_send", `{"session_key":"agent:cue:acc_1:sess_1","text":"hi"}`, true); got != "ran after a card" {
		t.Errorf("ConfirmEach on session rule: got %q, want ran after a card", got)
	}
	if got := outcome(ConfirmEach(ctx), rNormal, "club_agent_start", `{"account_id":"acc_2","text":"hi"}`, true); got != "ran after a card" {
		t.Errorf("ConfirmEach on account rule: got %q, want ran after a card", got)
	}

	// 5. Red line: repeatedField always returns Deny even when a matching UserRule is active.
	for _, dupArgs := range []string{
		`{"session_key":"agent:cue:acc_1:sess_1","SESSION_KEY":"agent:cue:acc_1:sess_1","text":"hi"}`,
		`{"session_key":"agent:cue:acc_2:sess_9","Session_Key":"agent:cue:acc_9:sess_9","text":"hi"}`,
		`{"account_id":"acc_2","ACCOUNT_ID":"acc_9","text":"hi"}`,
	} {
		res := rNormal.Call(ctx, "club_agent_send", json.RawMessage(dupArgs), func(context.Context, string, json.RawMessage) bool { return true })
		if !res.IsError || res.ErrorKind != proto.ToolErrorDeclined {
			t.Errorf("repeatedField %s: expected declined error, got %+v", dupArgs, res)
		}
	}
}

func TestClubUserRuleTTLAndMaxUsesAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	curNow := now
	oldNow := userRuleNow
	userRuleNow = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}
	setNow := func(t time.Time) {
		clockMu.Lock()
		curNow = t
		clockMu.Unlock()
	}
	defer func() { userRuleNow = oldNow }()

	// 1. TTL expiration fallback to Confirm.
	rule, err := ClubSessionRule("agent:cue:acc_1:sess_ttl")
	if err != nil {
		t.Fatal(err)
	}
	rule.At = now
	rule.ExpiresAt = now.Add(10 * time.Minute)

	r := NewRegistry(nil)
	_ = r.Add(clubTestConn{fake: &fake{id: "club", status: Status{State: StateOK}}})
	if err := r.SetUserRules([]UserRule{rule}); err != nil {
		t.Fatal(err)
	}
	args := `{"session_key":"agent:cue:acc_1:sess_ttl","text":"ping"}`
	if got := outcome(ctx, r, "club_agent_send", args, true); got != "ran" {
		t.Fatalf("before expiry: got %q, want ran", got)
	}
	// Re-applying SetUserRules on a TTL-only rule after use must keep it valid and active until ExpiresAt.
	if err := r.SetUserRules([]UserRule{rule}); err != nil {
		t.Fatalf("reapply TTL-only rule: %v", err)
	}
	if got := outcome(ctx, r, "club_agent_send", args, true); got != "ran" {
		t.Fatalf("before expiry after reapply: got %q, want ran", got)
	}
	setNow(now.Add(10 * time.Minute)) // exact boundary: expired
	if got := outcome(ctx, r, "club_agent_send", args, true); got != "ran after a card" {
		t.Fatalf("at expiry: got %q, want ran after a card", got)
	}
	setNow(now.Add(15 * time.Minute))
	if got := outcome(ctx, r, "club_agent_send", args, true); got != "ran after a card" {
		t.Fatalf("after expiry: got %q, want ran after a card", got)
	}

	// 2. MaxUses atomic consumption, ConfirmAlways/ConfirmEach non-consumption, and concurrent race test.
	setNow(now)
	limited, _ := ClubSessionRule("agent:cue:acc_1:sess_turns")
	limited.At = now
	limited.ExpiresAt = now.Add(time.Hour)
	limited.MaxUses = 5

	var usedEvents []int
	var usedMu sync.Mutex
	r.SetUserRuleUsed(func(_ context.Context, id string, used int) {
		usedMu.Lock()
		if id == limited.ID() {
			usedEvents = append(usedEvents, used)
		}
		usedMu.Unlock()
	})
	if err := r.SetUserRules([]UserRule{limited}); err != nil {
		t.Fatal(err)
	}
	turnArgs := `{"session_key":"agent:cue:acc_1:sess_turns","text":"turn"}`

	// ConfirmEach must clamp to Confirm and NOT consume a turn.
	if got := outcome(ConfirmEach(ctx), r, "club_agent_send", turnArgs, true); got != "ran after a card" || r.UserRuleUsedCount(limited.ID()) != 0 {
		t.Fatalf("ConfirmEach consumed a turn: got %q, used=%d", got, r.UserRuleUsedCount(limited.ID()))
	}

	// 20 concurrent calls racing against MaxUses=5: exactly 5 must run unasked, 15 must ask on a card.
	var unasked, carded atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch outcome(ctx, r, "club_agent_send", turnArgs, true) {
			case "ran":
				unasked.Add(1)
			case "ran after a card":
				carded.Add(1)
			}
		}()
	}
	wg.Wait()
	if unasked.Load() != 5 || carded.Load() != 15 || r.UserRuleUsedCount(limited.ID()) != 5 {
		t.Fatalf("concurrent MaxUses=5: unasked=%d carded=%d used=%d (want 5, 15, 5)", unasked.Load(), carded.Load(), r.UserRuleUsedCount(limited.ID()))
	}
	usedMu.Lock()
	if len(usedEvents) != 5 {
		t.Fatalf("usedEvents count=%d, want 5: %v", len(usedEvents), usedEvents)
	}
	usedMu.Unlock()

	// Re-applying SetUserRules with another rule must not reset the exhausted counter.
	other, _ := ClubAccountRule("acc_other")
	if err := r.SetUserRules([]UserRule{limited, other}); err != nil {
		t.Fatal(err)
	}
	if got := outcome(ctx, r, "club_agent_send", turnArgs, true); got != "ran after a card" {
		t.Fatalf("exhausted rule after SetUserRules reapply: got %q, want ran after a card", got)
	}
}
