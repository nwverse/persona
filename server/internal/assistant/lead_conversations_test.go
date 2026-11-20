package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A direction's lead conversation (#11392): kept listed however quiet, not deleted while it leads, still in the tree
// when its conversation is missing; notes saves that cannot overwrite each other and that reach the lead only when it
// is idle; an approval of assistant.leaders that compares the whole list. All ids, names and notes are placeholders.

// A current lead is kept listed on its direction record's computer (else the assistant's), on a fresh install with no
// setting touched, and leaves the set when its direction ends.
func TestProfileKeepsCurrentLeads(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.Main, v.Leaders = "main-1", []string{"main-1", "lead-a", "lead-b"} })
	b := &s.directions
	b.mu.Lock()
	if !s.directionsLocked(ctx) {
		t.Fatal("directions unreadable")
	}
	b.m["lead-a"] = &direction{MachineID: "box", By: directionByUser, At: time.Now()} // lead-b has no record yet
	b.mu.Unlock()
	s.publish(ctx)
	want := []SessionRef{{MachineID: "server", SessionID: "main-1"}, {MachineID: "box", SessionID: "lead-a"},
		{MachineID: "server", SessionID: "lead-b"}}
	if p := f.lastSnapshot().Profile; p == nil || !slices.Equal(p.Keep, want) {
		t.Fatalf("keep: %+v", p)
	}
	if ended, err := s.endDirection(ctx, "lead-a"); err != nil || !ended {
		t.Fatalf("end: %v %v", ended, err)
	}
	s.publish(ctx)
	want = []SessionRef{{MachineID: "server", SessionID: "main-1"}, {MachineID: "server", SessionID: "lead-b"}}
	if p := f.lastSnapshot().Profile; p == nil || !slices.Equal(p.Keep, want) {
		t.Fatalf("keep after the direction ended: %+v", p)
	}
}

// Deleting a current lead is refused with the protected-conversation code and a message, in the owner's language,
// that names the way out; once its direction ends the same delete goes through.
func TestDeleteRefusesACurrentLead(t *testing.T) {
	ctx := context.Background()
	f := healthHub()
	s := newTestService(t, f)
	lead := "work-0000-0000"
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main, v.Leaders = "server", "main-0000-0000", []string{lead} })
	path := "/api/v1/assistant/conversations/" + lead
	wayOut := map[i18n.Lang]string{i18n.En: "Unpin it", i18n.ZhHans: "取消常驻", i18n.Ja: "常駐をやめて"}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		f.mu.Lock()
		f.hello.Locale = string(l)
		f.mu.Unlock()
		w := serve(s, "DELETE", path, `{"machine_id":"server"}`)
		var body struct{ Code, Error string }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusBadRequest || body.Code != "conversation_protected" ||
			body.Error != i18n.T(l, "assistant-conversation_delete.lead_protected") || !strings.Contains(body.Error, wayOut[l]) {
			t.Fatalf("%s: %d %s", l, w.Code, w.Body)
		}
	}
	if !slices.ContainsFunc(s.view(ctx, false).Conversations, func(c proto.Session) bool { return c.SessionID == lead }) {
		t.Fatal("the refused delete took the lead off the list")
	}
	if ended, err := s.endDirection(ctx, lead); err != nil || !ended {
		t.Fatalf("end: %v %v", ended, err)
	}
	if w := serve(s, "DELETE", path, `{"machine_id":"server"}`); w.Code != http.StatusOK {
		t.Fatalf("delete after the direction ended: %d %s", w.Code, w.Body)
	}
}

// The direction tree lists a direction whose conversation its computer does not list, and one whose conversation was
// deleted (while it was not a lead, then made one again, or before deleting a lead was refused), each with its name
// and tasks.
func TestDirectionTreeListsDirectionsWhoseConversationIsMissing(t *testing.T) {
	e := treeEnv(t) // leads conv_lead_a and conv_lead_b
	ctx := context.Background()
	e.leaders("conv_lead_a")
	e.f.setSessions("server", proto.Session{MachineID: "server", SessionID: "conv_lead_b", Assistant: true, Status: proto.StatusIdle})
	if err := e.s.deleteConversation(ctx, "server", "conv_lead_b"); err != nil {
		t.Fatal(err)
	}
	e.f.setSessions("server", proto.Session{MachineID: "server", SessionID: treeMain, Assistant: true, Status: proto.StatusIdle})
	e.leaders("conv_lead_a", "conv_lead_b")
	e.syncLeaders()
	a := e.task("A's task", supervisedBy("conv_lead_a"))
	b := e.task("B's task", supervisedBy("conv_lead_b"))

	rec := httptest.NewRecorder()
	e.s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/directions/tree", nil))
	var tree directionTree
	if err := json.Unmarshal(rec.Body.Bytes(), &tree); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("tree: %d %s", rec.Code, rec.Body)
	}
	got := tree.Directions
	if len(got) != 3 || !got[2].Secretary {
		t.Fatalf("directions: %+v", got)
	}
	if got[0].ID != "conv_lead_a" || got[0].Name != "Android parity" || !slices.Equal(taskIDs(got[0]), []string{a}) {
		t.Fatalf("not listed: %+v", got[0])
	}
	if got[1].ID != "conv_lead_b" || got[1].Name != "Push relay" || !slices.Equal(taskIDs(got[1]), []string{b}) {
		t.Fatalf("deleted: %+v", got[1])
	}
	if _, listed := e.s.session(ctx, "server", "conv_lead_a"); listed {
		t.Fatal("conv_lead_a is listed")
	}
	if err := e.s.conversationIDAvailable(ctx, "conv_lead_b"); convCode(err) != "conversation_deleted" {
		t.Fatalf("conv_lead_b is not deleted: %v", err)
	}
}

// notesAnswer is a notes PUT's answer: the notes kept and their time, or the 409's code and what is stored.
type notesAnswer struct {
	Code    string     `json:"code"`
	Error   string     `json:"error"`
	Notes   string     `json:"notes"`
	NotesAt *time.Time `json:"notes_at"`
}

func putNotesAnswer(t *testing.T, s *Service, body string, status int) notesAnswer {
	t.Helper()
	rec := putNotes(t, s, "conv_lead", body, false)
	var out notesAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != status || err != nil {
		t.Fatalf("PUT %s: %d %s, want %d", body, rec.Code, rec.Body, status)
	}
	return out
}

// listedNotesAt is conv_lead's notes_at exactly as GET /api/v1/assistant/leaders writes it.
func listedNotesAt(t *testing.T, s *Service) string {
	t.Helper()
	return strings.Trim(string(listedNotesAtRaw(t, s, "conv_lead")), `"`)
}

// listedNotesAtRaw is direction id's notes_at field as GET /api/v1/assistant/leaders writes it (nil: left out).
func listedNotesAtRaw(t *testing.T, s *Service, id string) json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/leaders", nil))
	var list struct {
		Leaders []struct {
			ID      string          `json:"id"`
			NotesAt json.RawMessage `json:"notes_at"`
		} `json:"leaders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, l := range list.Leaders {
		if l.ID == id {
			return l.NotesAt
		}
	}
	t.Fatalf("%s not listed: %s", id, rec.Body)
	return nil
}

// A notes save that says which notes_at it started from is refused with what is stored when that is no longer it,
// and changes nothing; the current value is accepted as listed and as a client writes it again at millisecond
// precision; without notes_at a save is not checked.
func TestDirectionNotesSaveChecksNotesAt(t *testing.T) {
	s, _ := notesEnv(t)
	ctx := context.Background()
	// Two editors open the direction with no notes (null); the first save wins, the second is refused.
	first := putNotesAnswer(t, s, `{"notes":"Readings: the store build number.","notes_at":null}`, http.StatusOK)
	if first.NotesAt == nil || first.NotesAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("first save's time: %+v", first)
	}
	stale := putNotesAnswer(t, s, `{"notes":"Never deploy on Fridays.","notes_at":null}`, http.StatusConflict)
	if stale.Code != codeNotesChanged || stale.Error != i18n.T(i18n.Default, "assistant-direction_notes.changed") ||
		stale.Notes != "Readings: the store build number." || stale.NotesAt == nil || !stale.NotesAt.Equal(*first.NotesAt) {
		t.Fatalf("stale null: %+v", stale)
	}
	if d := s.directionOf("conv_lead"); d.Notes != "Readings: the store build number." || !d.NotesAt.Equal(*first.NotesAt) {
		t.Fatalf("a refused save changed the notes: %+v", d)
	}
	// The value as listed, and the same instant written again at millisecond precision in another zone.
	second := putNotesAnswer(t, s, `{"notes":"Readings: v2.","notes_at":"`+listedNotesAt(t, s)+`"}`, http.StatusOK)
	again := second.NotesAt.In(time.FixedZone("", 8*3600)).Format("2006-01-02T15:04:05.000Z07:00")
	third := putNotesAnswer(t, s, `{"notes":"Readings: v3.","notes_at":"`+again+`"}`, http.StatusOK)
	if !third.NotesAt.After(*second.NotesAt) || !second.NotesAt.After(*first.NotesAt) {
		t.Fatalf("times: %v %v %v", first.NotesAt, second.NotesAt, third.NotesAt)
	}
	// An editor that started from the first value is refused with the third.
	old := putNotesAnswer(t, s, `{"notes":"Readings: old.","notes_at":"`+first.NotesAt.Format(time.RFC3339Nano)+`"}`, http.StatusConflict)
	if old.Notes != "Readings: v3." || !old.NotesAt.Equal(*third.NotesAt) {
		t.Fatalf("stale time: %+v", old)
	}
	// Without notes_at: saved as before. A notes_at that is not a time: a bad request.
	if got := putNotesAnswer(t, s, `{"notes":"Readings: v4."}`, http.StatusOK); got.Notes != "Readings: v4." {
		t.Fatalf("unchecked: %+v", got)
	}
	putNotesAnswer(t, s, `{"notes":"x","notes_at":"yesterday"}`, http.StatusBadRequest)
	// A record from before (a finer time) still matches the value a client wrote again at millisecond precision.
	fine := time.Date(2026, 11, 20, 1, 2, 3, 456789123, time.UTC)
	s.directions.mu.Lock()
	s.directions.m["conv_lead"].NotesAt = &fine
	s.directions.mu.Unlock()
	putNotesAnswer(t, s, `{"notes":"Readings: v5.","notes_at":"2026-11-20T01:02:03.456Z"}`, http.StatusOK)
	// Cleared: an editor that still holds a time is refused with no notes (null).
	cleared := putNotesAnswer(t, s, `{"notes":""}`, http.StatusOK)
	if cleared.NotesAt != nil {
		t.Fatalf("cleared: %+v", cleared)
	}
	if got := putNotesAnswer(t, s, `{"notes":"x","notes_at":"`+again+`"}`, http.StatusConflict); got.Notes != "" || got.NotesAt != nil {
		t.Fatalf("stale after a clear: %+v", got)
	}
	// Two saves in the same millisecond never share a time.
	at := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	if next := nextNotesAt(&at, at.Add(300*time.Microsecond)); !next.Equal(at.Add(time.Millisecond)) {
		t.Fatalf("next = %v", next)
	}
	if _, _, err := s.setDirectionNotes(ctx, "nope", "x", nil); err != errNoDirection {
		t.Fatalf("unknown: %v", err)
	}
}

// Notes saved while the lead works leave its policy as its process runs it, so the owner's next message brings the
// node no new role to refuse; the first send that finds the lead idle carries them, also after a manager restart.
func TestDirectionNotesWaitForAnIdleLead(t *testing.T) {
	s, f := notesEnv(t)
	ctx := context.Background()
	lead := func(c proto.Session) {
		c.SessionID, c.MachineID, c.Assistant = "conv_lead", "server", true
		f.setSessions("server", c)
	}
	policyOf := func(s *Service) string {
		t.Helper()
		return dispatchedPolicy(t, s, "conv_lead")
	}
	lead(proto.Session{Status: proto.StatusIdle})
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v1.", nil); err != nil {
		t.Fatal(err)
	}
	v1 := policyOf(s)
	if !strings.HasPrefix(v1, prompts.RenderSupervisionInstructions()+"\n\n") || !strings.Contains(v1, "Readings: v1.") {
		t.Fatalf("idle: %q", v1)
	}
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v2.", nil); err != nil {
		t.Fatal(err)
	}
	busy := []proto.Session{{Status: proto.StatusWorking}, {Status: proto.StatusBlocked}, {Status: proto.StatusIdle, Starting: true},
		{Status: proto.StatusIdle, PendingInteractions: 1}, {Status: proto.StatusIdle, Compaction: &proto.Compaction{State: proto.CompactionRunning}},
		{Status: proto.StatusIdle, Background: []proto.BackgroundTask{{ID: "bg1"}}}}
	for _, c := range busy {
		lead(c)
		if got := policyOf(s); got != v1 {
			t.Fatalf("busy %+v: the policy changed to %q", c, got)
		}
	}
	// A manager restart reads the block in effect from the direction's record.
	lead(proto.Session{Status: proto.StatusWorking})
	s2 := newServiceOn(t, f, s.store())
	if got := policyOf(s2); got != v1 {
		t.Fatalf("after a restart: %q", got)
	}
	lead(proto.Session{Status: proto.StatusIdle})
	v2 := policyOf(s2)
	if !strings.Contains(v2, "Readings: v2.") || strings.Contains(v2, "Readings: v1.") {
		t.Fatalf("first idle send: %q", v2)
	}
	lead(proto.Session{Status: proto.StatusWorking})
	if got := policyOf(s2); got != v2 {
		t.Fatalf("busy after the idle send: %q", got)
	}
	// Another leader's policy, and a leader without notes, are untouched.
	if other := mustSupervisionInstructions(t, s2, ctx, "server", "conv_other"); other != prompts.RenderSupervisionInstructions() {
		t.Fatal("another leader's policy changed")
	}
}

// leadID is the i-th of six 36-character conversation ids, as the launchers make them: the approval card shows only
// the first three of a list and the start of the fourth.
func leadID(i int) string {
	return fmt.Sprintf("%08d-0000-4000-8000-%012d", i+1, i+1)
}

// An assistant.leaders card raised against one list is refused as stale when the owner changed the list before
// approving, also where the card's clipped text reads the same; the list stays as the owner left it.
func TestLeadersApprovalComparesTheWholeList(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	ids := make([]string, 6)
	var listed []proto.Session
	for i := range ids {
		ids[i] = leadID(i)
		listed = append(listed, proto.Session{SessionID: ids[i], MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	}
	f.setSessions("server", listed...)
	setSettings(t, s, func(v *Settings) { v.Main, v.Leaders = "main-1", slices.Clone(ids[:4]) })

	// propose has the assistant set value and, once the card is up, runs owner (the owner's own tap) and approves.
	propose := func(value []string, owner func()) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "set", "key": "assistant.leaders", "value": value})
		out := make(chan string, 1)
		go func() {
			r := s.Tool(ctx, ToolCall{Name: "persona_settings", Arguments: raw, MachineID: "server", SessionID: "main-1"})
			out <- r.Content[0].Text
		}()
		q, ok := awaitCard(s)
		if !ok {
			t.Fatal("no card")
		}
		if owner != nil {
			owner()
			// The card's text alone does not show the change: it is what the same proposal reads now.
			v, _ := json.Marshal(value)
			if fresh, err := s.setLeaders(ctx, v); err != nil || fresh.apply == nil || fresh.card(s.lang(ctx)) != q.Summary {
				t.Fatalf("the card text differs, so this does not test the whole-list comparison: %v", err)
			}
		}
		allow := true
		answer, _ := json.Marshal(proto.Resolution{Allow: &allow})
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: answer}); err != nil {
			t.Fatal(err)
		}
		select {
		case text := <-out:
			return text
		case <-time.After(5 * time.Second):
			t.Fatal("the tool did not return")
			return ""
		}
	}
	leaders := func() []string { return s.settings(ctx).Leaders }

	// Four leaders; the owner pins a fifth while the card waits.
	out := propose(append(slices.Clone(ids[:4]), ids[5]), func() {
		if _, err := s.makeDirection(ctx, ids[4], "", "", ""); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "changed after this card was prepared") || !slices.Equal(leaders(), ids[:5]) {
		t.Fatalf("pinned meanwhile: %s %v", out, leaders())
	}
	// Five leaders; the owner unpins the fifth while the card waits.
	out = propose(slices.Clone(ids), func() {
		if ended, err := s.endDirection(ctx, ids[4]); err != nil || !ended {
			t.Fatalf("end: %v %v", ended, err)
		}
	})
	if !strings.Contains(out, "changed after this card was prepared") || !slices.Equal(leaders(), ids[:4]) {
		t.Fatalf("unpinned meanwhile: %s %v", out, leaders())
	}
	// Nothing changed meanwhile: the approval applies.
	want := append(slices.Clone(ids[:4]), ids[5])
	if out = propose(want, nil); !strings.HasPrefix(out, "assistant.leaders is") || !slices.Equal(leaders(), want) {
		t.Fatalf("approved: %s %v", out, leaders())
	}
}

// listLeads lists rows as Persona conversations on the assistant's computer (and nothing else there).
func listLeads(f *fakeHub, rows ...proto.Session) {
	for i := range rows {
		rows[i].MachineID, rows[i].Assistant = "server", true
	}
	f.setSessions("server", rows...)
}

// ownerParams are what the hub asks for before it sends the owner's message params (nil: "go on") to conversation id
// (assistant.send_params, as hub/send.go does): the role policy they carry, or their refusal. How the send went is the
// test's to tell (hubSent).
func ownerParams(t *testing.T, s *Service, id string, params map[string]any) (string, error) {
	t.Helper()
	b := &BusHub{svc: s, helloed: make(chan struct{})}
	close(b.helloed) // the hub's hello is answered: the request does not wait for it
	if params == nil {
		params = map[string]any{"text": "go on"}
	}
	raw, err := json.Marshal(assistantwire.SendParams{MachineID: "server", SessionID: id, Params: params, Human: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := b.request(context.Background(), assistantwire.MethodSendParams, raw)
	if err != nil {
		return "", err
	}
	sent, _ := out.(map[string]any)["params"].(map[string]any)
	policy, _ := sent["supervision_instructions"].(string)
	return policy, nil
}

// ownerPolicy is the role policy the owner's next message to conversation id carries; the hub has yet to tell how it
// went (hubSent).
func ownerPolicy(t *testing.T, s *Service, id string) string {
	t.Helper()
	policy, err := ownerParams(t, s, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// hubSent is the hub's notice of how the owner's message to conversation id went (session.assistant_sent).
func hubSent(s *Service, id, outcome string) {
	s.ConversationSent(context.Background(), ConversationSent{MachineID: "server", SessionID: id, Outcome: outcome})
}

// dispatchedPolicy is the role policy the owner's next message to conversation id is sent with, which the node takes.
func dispatchedPolicy(t *testing.T, s *Service, id string) string {
	t.Helper()
	policy := ownerPolicy(t, s, id)
	hubSent(s, id, assistantwire.ConversationDelivered)
	return policy
}

// managerSend is the manager's own message to conversation id (call), with the role policy it carried and the node's
// answer.
func managerSend(s *Service, id string) (string, error) {
	p := map[string]any{"session_id": id, "text": "a supervision message"}
	err := s.call(context.Background(), "server", DeviceSessionSend, p, nil)
	policy, _ := p["supervision_instructions"].(string)
	return policy, err
}

// nodePolicy is the role policy the node's own next message to conversation id runs on (assistant.model), which is
// the block the lead is taken to run: asking moves nothing.
func nodePolicy(t *testing.T, s *Service, id string) string {
	t.Helper()
	out, ok, err := s.NodeAnswer(context.Background(), "server", "assistant.model", json.RawMessage(`{"session_id":"`+id+`"}`))
	if !ok || err != nil {
		t.Fatalf("assistant.model: %v %v", ok, err)
	}
	m, _ := out.(map[string]any)
	policy, _ := m["supervision_instructions"].(string)
	if policy == "" {
		t.Fatalf("assistant.model gave no role policy: %v", out)
	}
	return policy
}

// nodeRefuses is the node's answer to a send it refuses before it reaches the conversation, as it refuses a new role
// for a process at work.
func nodeRefuses(string, json.RawMessage) (any, error) {
	return nil, &rpc.Error{Code: "busy", Message: "busy", Data: json.RawMessage(`{"delivered":"none"}`)}
}

func nodeTakes(string, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil }

// A direction ended and made again does not bring back the notes block it had before it ended: once unpinned, an idle
// send restarts the conversation on the plain policy (it still supervises its task), so after the owner clears the
// notes and pins it again, a message that finds it working is sent on that plain policy, with no role change for the
// node to refuse.
func TestDirectionNotesInEffectFollowTheConversationAcrossItsEnd(t *testing.T) {
	s, f := notesEnv(t)
	ctx := context.Background()
	plain := prompts.RenderSupervisionInstructions()
	todo, err := s.createTodo(ctx, "Ship the build", "", "conv_lead", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, todo.ID, "conv_lead", []string{"Shipped"}, 30)
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: the store build number.", nil); err != nil {
		t.Fatal(err)
	}
	if got := dispatchedPolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: the store build number.") {
		t.Fatalf("lead: %q", got)
	}
	if ended, err := s.endDirection(ctx, "conv_lead"); err != nil || !ended {
		t.Fatalf("end: %v %v", ended, err)
	}
	if got := dispatchedPolicy(t, s, "conv_lead"); got != plain {
		t.Fatalf("unpinned: %q", got)
	}
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.makeDirection(ctx, "conv_lead", "", "", ""); err != nil {
		t.Fatal(err)
	}
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
	if got := dispatchedPolicy(t, s, "conv_lead"); got != plain {
		t.Fatalf("pinned again while it works: the policy changed to %q", strings.TrimPrefix(got, plain))
	}
}

// A lead with no block in effect recorded yet (from before #11392, or never sent to since) keeps what it runs on when
// the owner's first save or first clear comes while it works; the first idle send then carries the notes as they are.
func TestDirectionNotesFirstSaveWhileTheLeadWorks(t *testing.T) {
	s, f := notesEnv(t)
	ctx := context.Background()
	plain := prompts.RenderSupervisionInstructions()
	// conv_other has notes from before any block in effect was recorded: every send so far carried them.
	at := time.Now().UTC()
	s.directions.mu.Lock()
	s.directions.m["conv_other"].Notes, s.directions.m["conv_other"].NotesAt = "Infra rule: back up first.", &at
	s.directions.mu.Unlock()
	withOld := plain + "\n\n" + prompts.RenderDirectionNotes(s.directionPrompt(ctx, "conv_other"))
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking},
		proto.Session{SessionID: "conv_other", Status: proto.StatusWorking})
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v1.", nil); err != nil { // its first notes
		t.Fatal(err)
	}
	if _, _, err := s.setDirectionNotes(ctx, "conv_other", "", nil); err != nil { // its first clear
		t.Fatal(err)
	}
	if got := dispatchedPolicy(t, s, "conv_lead"); got != plain {
		t.Fatalf("first save while it works: %q", strings.TrimPrefix(got, plain))
	}
	if got := dispatchedPolicy(t, s, "conv_other"); got != withOld {
		t.Fatalf("first clear while it works: %q", strings.TrimPrefix(got, plain))
	}
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle},
		proto.Session{SessionID: "conv_other", Status: proto.StatusIdle})
	if got := dispatchedPolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: v1.") {
		t.Fatalf("idle: %q", got)
	}
	if got := dispatchedPolicy(t, s, "conv_other"); got != plain {
		t.Fatalf("idle after the clear: %q", got)
	}
}

// A notes_at is never issued twice, also across a clear: an editor that opened the notes before the clear is refused
// even when the next save comes in the same millisecond as the one it read, and an older record keeps its time too.
func TestNotesAtIsNeverIssuedTwiceAcrossAClear(t *testing.T) {
	s, _ := notesEnv(t)
	clock := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	s.directions.mu.Lock()
	s.directions.now = func() time.Time { return clock }
	s.directions.mu.Unlock()
	first := putNotesAnswer(t, s, `{"notes":"Readings: A."}`, http.StatusOK)
	putNotesAnswer(t, s, `{"notes":""}`, http.StatusOK)
	second := putNotesAnswer(t, s, `{"notes":"Readings: B."}`, http.StatusOK)
	if !second.NotesAt.After(*first.NotesAt) {
		t.Fatalf("issued again: %v then %v", first.NotesAt, second.NotesAt)
	}
	stale := putNotesAnswer(t, s, `{"notes":"Readings: C.","notes_at":"`+first.NotesAt.Format(time.RFC3339Nano)+`"}`, http.StatusConflict)
	if stale.Notes != "Readings: B." {
		t.Fatalf("stale editor: %+v", stale)
	}
	// An older record has only NotesAt: cleared and saved again at the same instant, it still moves on.
	old := clock.Add(time.Hour)
	s.directions.mu.Lock()
	d := s.directions.m["conv_lead"]
	d.Notes, d.NotesAt, d.NotesLastAt = "Readings: old.", &old, nil
	s.directions.now = func() time.Time { return old }
	s.directions.mu.Unlock()
	putNotesAnswer(t, s, `{"notes":""}`, http.StatusOK)
	if again := putNotesAnswer(t, s, `{"notes":"Readings: D."}`, http.StatusOK); !again.NotesAt.After(old) {
		t.Fatalf("older record issued again: %v", again.NotesAt)
	}
}

// A notes block a send carried counts as given only once the node took that send. When the node refuses it (the lead
// had just started a turn its session row did not show yet), the block before it is put back, so the next messages of
// that turn keep the policy the lead runs on. The owner's message is settled by the hub's notice of how it sent it,
// the manager's own send by the node's answer; a send the node took keeps the new block.
func TestDirectionNotesCountAsGivenOnlyOnceTheNodeTookTheSend(t *testing.T) {
	s, f := notesEnv(t)
	ctx := context.Background()
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v1.", nil); err != nil {
		t.Fatal(err)
	}
	v1 := dispatchedPolicy(t, s, "conv_lead")
	// The owner saves v2 and sends; the row still reads idle, but the node already sees a turn and refuses it.
	if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v2.", nil); err != nil {
		t.Fatal(err)
	}
	if got := ownerPolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: v2.") {
		t.Fatalf("idle send: %q", got)
	}
	hubSent(s, "conv_lead", assistantwire.ConversationFailed)
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
	if got := nodePolicy(t, s, "conv_lead"); got != v1 {
		t.Fatalf("the rest of the turn after a refused message: %q", got)
	}
	// The manager's own send, refused the same way.
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
	f.answerNode(DeviceSessionSend, nodeRefuses)
	if _, err := managerSend(s, "conv_lead"); err == nil {
		t.Fatal("the refused send went")
	}
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
	if got := nodePolicy(t, s, "conv_lead"); got != v1 {
		t.Fatalf("the rest of the turn after a refused manager send: %q", got)
	}
	// Taken: the new block stays.
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
	f.answerNode(DeviceSessionSend, nodeTakes)
	if _, err := managerSend(s, "conv_lead"); err != nil {
		t.Fatal(err)
	}
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
	if got := nodePolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: v2.") {
		t.Fatalf("after a taken send: %q", got)
	}
}

// A notes block is settled by the outcome of the send that carried it, not by whichever outcome for its conversation
// comes first (#11392). The manager's own send, carrying the block the lead runs on, is in flight while the owner
// saves new notes and sends a message that moves the block; the two outcomes then come in either order, and the lead
// is taken to run the block of the send the node took.
func TestDirectionNotesSettledByTheSendThatCarriedThem(t *testing.T) {
	for _, tc := range []struct {
		name         string
		managerTaken bool   // the earlier send: the manager's
		owner        string // the later: the owner's message, as the hub reports it
		want         string // the notes the lead runs after both
	}{
		{"the earlier send taken then the later refused", true, assistantwire.ConversationFailed, "Readings: v1."},
		{"the earlier send refused then the later taken", false, assistantwire.ConversationDelivered, "Readings: v2."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := notesEnv(t)
			ctx := context.Background()
			listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
			if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v1.", nil); err != nil {
				t.Fatal(err)
			}
			v1 := dispatchedPolicy(t, s, "conv_lead")
			// The manager's send is in flight: its policy step is done and the node has it.
			arrived, release := make(chan struct{}), make(chan struct{})
			f.mu.Lock()
			f.beforeSend = func() { close(arrived); <-release }
			f.mu.Unlock()
			if tc.managerTaken {
				f.answerNode(DeviceSessionSend, nodeTakes)
			} else {
				f.answerNode(DeviceSessionSend, nodeRefuses)
			}
			type sent struct {
				policy string
				err    error
			}
			done := make(chan sent, 1)
			go func() {
				policy, err := managerSend(s, "conv_lead")
				done <- sent{policy, err}
			}()
			<-arrived
			if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v2.", nil); err != nil {
				t.Fatal(err)
			}
			if got := ownerPolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: v2.") {
				t.Fatalf("the owner's message: %q", got)
			}
			close(release)
			if m := <-done; m.policy != v1 || (m.err == nil) != tc.managerTaken {
				t.Fatalf("the manager's send: %v, carrying %q", m.err, m.policy)
			}
			hubSent(s, "conv_lead", tc.owner)
			listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
			if got := nodePolicy(t, s, "conv_lead"); !strings.Contains(got, tc.want) {
				t.Fatalf("taken to run %q, want %s", strings.TrimPrefix(got, prompts.RenderSupervisionInstructions()), tc.want)
			}
		})
	}
}

// Only a send whose outcome is reported moves the notes block in effect, and each such send reports it (#11392):
// assistant.answer_again through call, and a message whose parameters were made but that was refused before it left
// as not taken; the node's own request for what its message runs on (assistant.model) does not move it. Each time,
// the block the lead is then taken to run is the one its process got.
func TestDirectionNotesMovedOnlyBySendsThatReportTheirOutcome(t *testing.T) {
	ctx := context.Background()
	start := func(t *testing.T, row proto.Session) (*Service, *fakeHub, string) {
		t.Helper()
		s, f := notesEnv(t)
		row.SessionID, row.Agent = "conv_lead", "claude"
		listLeads(f, row)
		if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v1.", nil); err != nil {
			t.Fatal(err)
		}
		return s, f, dispatchedPolicy(t, s, "conv_lead")
	}
	saveV2 := func(t *testing.T, s *Service) {
		t.Helper()
		if _, _, err := s.setDirectionNotes(ctx, "conv_lead", "Readings: v2.", nil); err != nil {
			t.Fatal(err)
		}
	}
	working := func(f *fakeHub) {
		listLeads(f, proto.Session{SessionID: "conv_lead", Agent: "claude", Status: proto.StatusWorking})
	}

	t.Run("answer again", func(t *testing.T) {
		s, f, _ := start(t, proto.Session{Status: proto.StatusDone})
		const ask = "Summarize today's usage."
		if _, err := ownerParams(t, s, "conv_lead", map[string]any{"client_msg_id": "app-user-input", "text": ask}); err != nil {
			t.Fatal(err)
		}
		hubSent(s, "conv_lead", assistantwire.ConversationDelivered)
		// The turn ends without a reply, and the owner saves v2 meanwhile: the manager asks for the reply again.
		saveV2(t, s)
		f.setItems(
			proto.Item{ID: "u1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: ask, ClientMsgID: "app-user-input"},
			proto.Item{ID: "t2#0", Seq: 2, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "tool", ToolName: "Bash"},
			proto.Item{ID: "r3#0", Seq: 3, Role: "user", Kind: proto.KindToolResult, ToolUseID: "tool", Text: "24 requests"},
		)
		var again []string
		f.answerNode(assistantwire.DeviceAssistantAnswerAgain, func(_ string, raw json.RawMessage) (any, error) {
			var p struct {
				Policy string `json:"supervision_instructions"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			again = append(again, p.Policy)
			return map[string]any{}, nil
		})
		s.assistantTurnSettled(ctx, "server", "conv_lead")
		if len(again) != 1 {
			t.Fatalf("answer_again sends: %d", len(again))
		}
		// The owner's next message fails before it reaches the conversation (its computer is briefly offline).
		ownerPolicy(t, s, "conv_lead")
		hubSent(s, "conv_lead", assistantwire.ConversationFailed)
		working(f)
		if got := nodePolicy(t, s, "conv_lead"); got != again[0] {
			t.Fatalf("taken to run %q, but answer_again gave it %q", got, again[0])
		}
	})

	t.Run("the owner's message refused before it left", func(t *testing.T) {
		s, f, v1 := start(t, proto.Session{Status: proto.StatusIdle})
		saveV2(t, s)
		if _, err := ownerParams(t, s, "conv_lead", map[string]any{"text": "go on", "model": "not a model id!"}); err == nil {
			t.Fatal("a bad model was taken")
		}
		working(f)
		if got := nodePolicy(t, s, "conv_lead"); got != v1 {
			t.Fatalf("taken to run %q after a refused message", got)
		}
	})

	t.Run("the manager's send refused before it left", func(t *testing.T) {
		s, f, v1 := start(t, proto.Session{Status: proto.StatusIdle})
		saveV2(t, s)
		f.mu.Lock()
		f.sessions["mac"], f.catalogNodes = nil, true // another of the owner's computers, the only one the provider serves
		f.mu.Unlock()
		addProvider(t, s, `{"name":"box","base_url":"https://gw.example.com","model":"m1","api_key":"sk-test-box-000000","share":"selected","scope_machines":["mac"]}`)
		p := map[string]any{"session_id": "conv_lead", "launcher": "box", "text": "a supervision message"}
		if err := s.call(ctx, "server", DeviceSessionSend, p, nil); codeOf(err) != proto.CodeLauncherOutOfScope {
			t.Fatalf("a send on a launcher out of scope: %v", err)
		}
		working(f)
		if got := nodePolicy(t, s, "conv_lead"); got != v1 {
			t.Fatalf("taken to run %q after a refused send", got)
		}
	})

	t.Run("the node's own message", func(t *testing.T) {
		s, f, v1 := start(t, proto.Session{Status: proto.StatusIdle})
		saveV2(t, s)
		own := nodePolicy(t, s, "conv_lead") // the node asks what its own message runs on, and sends it
		if own != v1 {
			t.Fatalf("the node's own message moved the block: %q", own)
		}
		// The owner's next message moves it, and fails before it reaches the conversation.
		if got := ownerPolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: v2.") {
			t.Fatalf("the owner's message: %q", got)
		}
		hubSent(s, "conv_lead", assistantwire.ConversationFailed)
		working(f)
		if got := nodePolicy(t, s, "conv_lead"); got != own {
			t.Fatalf("taken to run %q, but the node's own message gave it %q", got, own)
		}
	})
}

// A send whose outcome is never learned keeps the block it carried (#11392): the owner's message whose notice never
// comes (the hub answered a retry from its record without sending it), dropped once its notice can no longer come so
// that the hub's next notice settles the message it is about, and a message whose notice reaches a restarted manager.
func TestDirectionNotesKeptWhenTheOutcomeIsNeverLearned(t *testing.T) {
	s, f := notesEnv(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	clock := func(t time.Time) {
		s.directions.mu.Lock()
		s.directions.now = func() time.Time { return t }
		s.directions.mu.Unlock()
	}
	clock(at)
	save := func(s *Service, notes string) {
		t.Helper()
		if _, _, err := s.setDirectionNotes(ctx, "conv_lead", notes, nil); err != nil {
			t.Fatal(err)
		}
	}
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
	save(s, "Readings: v1.")
	dispatchedPolicy(t, s, "conv_lead")
	save(s, "Readings: v2.")
	v2 := ownerPolicy(t, s, "conv_lead") // no notice comes for it
	clock(at.Add(notesOwnerWait))
	save(s, "Readings: v3.")
	if got := ownerPolicy(t, s, "conv_lead"); !strings.Contains(got, "Readings: v3.") {
		t.Fatalf("the next message: %q", got)
	}
	hubSent(s, "conv_lead", assistantwire.ConversationFailed)
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
	if got := nodePolicy(t, s, "conv_lead"); got != v2 {
		t.Fatalf("taken to run %q, want the block of the message never reported", strings.TrimPrefix(got, prompts.RenderSupervisionInstructions()))
	}
	// A manager restart between a send and its outcome: the block is kept, and the restarted manager settles nothing.
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusIdle})
	save(s, "Readings: v4.")
	v4 := ownerPolicy(t, s, "conv_lead")
	s2 := newServiceOn(t, f, s.store())
	hubSent(s2, "conv_lead", assistantwire.ConversationFailed)
	listLeads(f, proto.Session{SessionID: "conv_lead", Status: proto.StatusWorking})
	if got := nodePolicy(t, s2, "conv_lead"); got != v4 || !strings.Contains(v4, "Readings: v4.") {
		t.Fatalf("after a restart: %q", strings.TrimPrefix(got, prompts.RenderSupervisionInstructions()))
	}
}

// GET /api/v1/assistant/leaders lists notes_at as null for a direction without notes, the value a save sends back, so
// two editors of an empty direction that both echo it are told apart: the first saves, the second gets 409.
func TestLeadersListNotesAtNullWithoutNotes(t *testing.T) {
	s, _ := notesEnv(t)
	listed := listedNotesAtRaw(t, s, "conv_lead")
	if listed == nil {
		t.Fatal("notes_at without notes is left out, want null")
	} else if string(listed) != "null" {
		t.Fatalf("notes_at without notes: %s, want null", listed)
	}
	putNotesAnswer(t, s, `{"notes":"Readings: A.","notes_at":`+string(listed)+`}`, http.StatusOK)
	if got := putNotesAnswer(t, s, `{"notes":"Readings: B.","notes_at":`+string(listed)+`}`, http.StatusConflict); got.Notes != "Readings: A." {
		t.Fatalf("second editor: %+v", got)
	}
}
