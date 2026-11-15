package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Sending an accepted task more (#7539). A task its supervisor accepted opens again only for what its own conversation
// sends its agent (sendReopens, closedTurnReopens): agent_send, agent_interrupt with then, the owner's message on its
// card. Such a send is noted before it goes (noteAgentSend), with the task, that acceptance and how long the send
// counts for it (its own wait and sendGrace), and the note is kept in the assistant's settings (sendsOutKey) as well as
// in memory: the agent's turn may start after a manager restart, when the node queued the text or the answer was lost
// with the manager. A send whose note cannot be kept does not go (errSendNotKept). How the send went decides
// (agentSendNote.sent, sendAnswered):
//
//   - taken: the task opens again now (reopenTodo, stored), and the note goes.
//   - definitely failed (the node refused it, or it never went out: rpc.ErrNotSent): it asked for nothing, and the note
//     goes.
//   - unknown (no answer within its wait, a dropped link, or the node says the text may have arrived: nodeMayHaveTaken):
//     the text may still arrive. The note stays until the send stops counting, and a turn of the agent that starts by
//     then opens the task (closedTurnReopens). If none does, the task stays closed.
//
// A turn of the agent that starts while such a send is out is held for the answer (closedTurnHeld), and so is its end
// (endHeld). Taken or unknown, the turn is the task's: the task opens and the turn is followed from then. Failed, with
// no other send of more for the task out or counting, it was other work: the task stays closed through it and its end.
// A manager that starts with notes the one before kept takes each as a send whose answer is unknown (loadSendsOut), and
// a turn held for one as the task's (recoverSendsOut).

// sendGrace is how long past its own wait a send of more for an accepted task still counts (agentSendNote.until): a
// turn of its agent that starts by then may be the one it asked for.
var sendGrace = time.Minute

// sendsOutKey is the settings key the sends of more for accepted tasks are kept under (saveSendsOut).
const sendsOutKey = "assistant.sends_out"

// agentSendNote is a send noteAgentSend recorded before it went; its caller says how it went (sent).
type agentSendNote struct {
	s    *Service
	key  [2]string // the agent: machine, session
	from string    // the conversation that sends it
	at   time.Time
	// task, doneAt: the accepted task the send asks more of, and that acceptance; "" for a send that reopens nothing.
	task   string
	doneAt time.Time
	// until: when the send stops counting for the task (its own wait, then sendGrace). answered: its answer is in and
	// is not sure (unknown), or the reopen it asked for could not be written: a turn of the agent that starts before
	// until opens the task. Under s.tasks.mu.
	until    time.Time
	answered bool
}

// sendsOut is what is out for one agent (tasksState.sendsOut): every send of more for its accepted task that still
// counts, one note each, and a turn of the agent held for them. A later send to the agent, for the task or not, never
// hides that turn.
type sendsOut struct {
	notes []*agentSendNote
	// held, heldAt: the task, and its acceptance, that a turn of the agent is held for; it started while a send of more
	// for that task was out ("": none). ended: that turn ended meanwhile, and its end was put off (endHeld).
	held   string
	heldAt time.Time
	ended  bool
}

// reopens: n asks an accepted task for more, and its answer decides what a turn of the agent that starts meanwhile is
// (closedTurnHeld): such a send has a bounded wait, its own (agent_send's liveSendWait). n may be nil.
func (n *agentSendNote) reopens() bool { return n != nil && n.task != "" }

// counts: n asks task id, accepted at doneAt (that acceptance, not a later one), for more, and still counts at now.
func (n *agentSendNote) counts(id string, doneAt, now time.Time) bool {
	return n.task == id && sameInstant(n.doneAt, doneAt) && now.Before(n.until)
}

// sameInstant: a and b are one moment, whether read from the database (to the microsecond) or kept in memory.
func sameInstant(a, b time.Time) bool { return a.Sub(b).Abs() < time.Millisecond }

// asking says whether, at now, a send of more for task id accepted at doneAt is out with its answer not in yet
// (pending), or answered unknown and still counting (answered). o may be nil.
func (o *sendsOut) asking(id string, doneAt, now time.Time) (pending, answered bool) {
	if o == nil {
		return false, false
	}
	for _, n := range o.notes {
		if n.counts(id, doneAt, now) {
			if n.answered {
				answered = true
			} else {
				pending = true
			}
		}
	}
	return pending, answered
}

// holds: the turn held here is held for task id accepted at doneAt. o may be nil.
func (o *sendsOut) holds(id string, doneAt time.Time) bool {
	return o != nil && o.held != "" && o.held == id && sameInstant(o.heldAt, doneAt)
}

// noteAgentSend records that conversation self is sending agent mid/sid text now (agent_send, agent_interrupt with
// then, or the owner's message on a task's card, routeToTaskAs), before the send goes: its turn may start before the
// call returns. ctx is the send's own: its deadline is the send's wait. When the agent still belongs to an accepted task
// that self may ask for more (sendReopens), decided now as self stands now, the note names that task and that
// acceptance, and it is kept (saveSendsOut) before the send goes. task: the one task the send is for (the card's), so
// that an agent handed to another task meanwhile reopens nothing; "" for whichever task the agent belongs to
// (agent_send, agent_interrupt). text: what the agent gets; a bare /exit or /quit asks the task for nothing (the node
// closes the agent, exitText), and reopens nothing.
//
// A note that could not be kept (errSendNotKept: the kept ones could not be read, or this one not written) would be
// lost with a manager restart while the node holds the text, so the send must not go: the note is taken back as a send
// that never went out, and the caller returns the error (the note is nil then).
func (s *Service) noteAgentSend(ctx context.Context, self, mid, sid, task, text string) (*agentSendNote, error) {
	n := &agentSendNote{s: s, key: [2]string{mid, sid}, from: self, at: time.Now()}
	if exitText(text) {
		return n, nil
	}
	t, ok := s.closedTaskOf(ctx, mid, sid)
	if !ok || task != "" && t.ID != task || t.DoneBy != db.DoneByReview || t.DoneAt == nil || !s.sendReopens(ctx, self, t) {
		return n, nil
	}
	wait := liveSendWait
	if d, ok := ctx.Deadline(); ok {
		wait = time.Until(d)
	}
	if err := s.loadSendsOut(ctx); err != nil { // what a manager before this one kept is written with it
		return nil, errSendNotKept{task: t.ID, cause: err}
	}
	n.task, n.doneAt, n.until = t.ID, *t.DoneAt, n.at.Add(max(wait, 0)+sendGrace)
	s.tasks.mu.Lock()
	o := s.tasks.sendsOut[n.key]
	if o == nil {
		o = &sendsOut{}
		s.tasks.sendsOut[n.key] = o
	}
	o.notes = append(o.notes, n)
	s.tasks.mu.Unlock()
	if err := s.saveSendsOut(ctx); err != nil {
		// It never goes out: a turn of the agent held for it meanwhile goes as for a send that failed, and the write
		// tried again there puts right one that went through after all.
		n.sent(ctx, rpc.NotSent(err))
		return nil, errSendNotKept{task: t.ID, cause: err}
	}
	return n, nil
}

// errSendNotKept: a send of more for accepted task task was not sent, because its note could not be kept
// (noteAgentSend). It does not wrap cause: the send never went, whatever cause says (a write's own timeout is not a
// send's answer that never came, startResultUnknown).
type errSendNotKept struct {
	task  string
	cause error
}

func (e errSendNotKept) Error() string {
	return fmt.Sprintf("nothing was sent: could not save that this send reopens accepted task %s (%v); try again", e.task, e.cause)
}

// sendOutcome is how a noted send went (agentSendNote.sent).
type sendOutcome int

const (
	sendTaken  sendOutcome = iota // the node took it
	sendUnsure                    // it may have reached the agent: no answer came, or the node could not say (sendUnknown)
	sendFailed                    // refused, or it never went out: it asked for nothing
)

// sent is how the noted send went (err: the send call's; the top of this file says what each outcome does).
func (n *agentSendNote) sent(ctx context.Context, err error) {
	if n == nil || n.task == "" {
		return
	}
	how := sendTaken
	if err != nil {
		how = sendFailed
		if sendUnknown(err) {
			how = sendUnsure
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	n.s.sendAnswered(ctx, n, how)
}

// sendAnswered is what n's answer makes of its task, under s.tasks.completionMu: a turn start opens the task under it
// too (one of the two does). A taken send opens the task now. A turn of the agent held for the task is the task's when
// the send was taken or its answer is unknown, or when it failed while an earlier send of more for the task, answered
// unknown, still counts: the task opens, and the turn is followed from here, its end too when it came meanwhile. A failed
// send leaves that turn held while another send for the task is still out, and as other work when none is: the task
// stays closed, and the turn's end is dropped. An unknown answer with no turn held keeps the note, for a turn that
// starts before the send stops counting.
func (s *Service) sendAnswered(ctx context.Context, n *agentSendNote, how sendOutcome) {
	s.tasks.completionMu.Lock()
	defer s.tasks.completionMu.Unlock()
	s.tasks.mu.Lock()
	o := s.tasks.sendsOut[n.key]
	n.answered = true
	if how == sendFailed && o != nil {
		o.notes = slices.DeleteFunc(o.notes, func(m *agentSendNote) bool { return m == n })
		if len(o.notes) == 0 && o.held == "" {
			delete(s.tasks.sendsOut, n.key)
		}
	}
	held := o.holds(n.task, n.doneAt)
	pending, answered := o.asking(n.task, n.doneAt, time.Now())
	s.tasks.mu.Unlock()
	take := held && (how != sendFailed || !pending && answered)
	drop := held && how == sendFailed && !pending && !answered
	switch {
	case how == sendTaken || take:
		s.sendsSettle(ctx, n.key, n.task, n.doneAt, n.from, take, false)
	case drop:
		s.tasks.mu.Lock()
		if o := s.tasks.sendsOut[n.key]; o.holds(n.task, n.doneAt) {
			o.held, o.heldAt, o.ended = "", time.Time{}, false
			if len(o.notes) == 0 {
				delete(s.tasks.sendsOut, n.key)
			}
		}
		s.tasks.mu.Unlock()
		s.saveSendsOut(ctx)
		s.logf("task %s: the turn its agent %s/%s started while more for it was being sent is other work: the send failed; left closed",
			n.task, n.key[0], short(n.key[1]))
	case how == sendFailed:
		s.saveSendsOut(ctx) // its note is gone
	}
}

// sendsSettle opens task id again for the sends of more to agent key that asked for it (from: the conversation that
// sent the one answered), under s.tasks.completionMu, when it is still closed on that acceptance (doneAt): a turn start
// or another answer may have opened it, or it moved (opened and closed again, the agent works for another task). Those
// sends are done with then; one whose reopen could not be written keeps counting, for the agent's next turn. take: the
// turn held for the task is the task's: it is followed from here, and its end, when it came meanwhile (look: in any
// case), is taken up again.
func (s *Service) sendsSettle(ctx context.Context, key [2]string, id string, doneAt time.Time, from string, take, look bool) {
	mid, sid := key[0], key[1]
	written := true
	if cur, ok := s.closedTaskOf(ctx, mid, sid); ok && cur.ID == id && cur.DoneAt != nil && sameInstant(*cur.DoneAt, doneAt) {
		if err := s.reopenTodo(ctx, id); err != nil {
			s.logf("task %s: reopen for what %s sent its agent: %v", id, short(from), err)
			written = false
		} else {
			s.logf("task %s: reopened: %s sent its agent %s/%s more", id, short(from), mid, short(sid))
		}
	}
	followed := false
	if take {
		if t, ok := s.taskOf(ctx, mid, sid); ok && t.ID == id {
			s.logf("task %s: the turn its agent %s/%s started while more for it was being sent is the task's: followed", id, mid, short(sid))
			s.taskWorks(ctx, t)
			followed = true
		} else {
			s.logf("task %s: the turn its agent %s/%s started while more for it was being sent is other work: left closed", id, mid, short(sid))
		}
	}
	ended := false
	s.tasks.mu.Lock()
	if o := s.tasks.sendsOut[key]; o != nil {
		if take && o.holds(id, doneAt) {
			ended = o.ended
			o.held, o.heldAt, o.ended = "", time.Time{}, false
		}
		if written {
			o.notes = slices.DeleteFunc(o.notes, func(m *agentSendNote) bool { return m.task == id && sameInstant(m.doneAt, doneAt) })
		}
		if len(o.notes) == 0 && o.held == "" {
			delete(s.tasks.sendsOut, key)
		}
	}
	s.tasks.mu.Unlock()
	s.saveSendsOut(ctx)
	if followed && (ended || look) {
		go s.taskTurnSettled(context.WithoutCancel(ctx), mid, sid)
	}
}

// sendsOpened: closed task t, accepted, was opened again by a turn of its agent mid/sid that started while a send of
// more for it still counted (closedTurnReopens, taskWorking), under s.tasks.completionMu. Those sends are done with,
// and so is a turn held for them before (that turn is over, or this is it, followed from here).
func (s *Service) sendsOpened(ctx context.Context, t Todo, mid, sid string) {
	key := [2]string{mid, sid}
	s.tasks.mu.Lock()
	o := s.tasks.sendsOut[key]
	if o == nil {
		s.tasks.mu.Unlock()
		return
	}
	if o.holds(t.ID, *t.DoneAt) {
		o.held, o.heldAt, o.ended = "", time.Time{}, false
	}
	o.notes = slices.DeleteFunc(o.notes, func(m *agentSendNote) bool { return m.task == t.ID && sameInstant(m.doneAt, *t.DoneAt) })
	if len(o.notes) == 0 && o.held == "" {
		delete(s.tasks.sendsOut, key)
	}
	s.tasks.mu.Unlock()
	s.saveSendsOut(ctx)
}

// endHeld: agent mid/sid ended a turn held for the answers of sends of more for its task (closedTurnReopens). The end
// is put off and noted, and the answer that decides the turn takes it up again once the task is open (sendsSettle);
// when the sends failed, the turn was other work, and its end is dropped with it. A turn held for sends that no longer
// count (each counts a little past its own wait) is let go: its end is a closed task's, and dropped. A turn the
// manager before this one held is looked at again as the manager starts (recoverSendsOut), its end included, so this
// reads only what is in memory: every turn's end comes here.
func (s *Service) endHeld(ctx context.Context, mid, sid string) bool {
	key := [2]string{mid, sid}
	s.tasks.mu.Lock()
	o := s.tasks.sendsOut[key]
	if o == nil || o.held == "" {
		s.tasks.mu.Unlock()
		return false
	}
	pending, answered := o.asking(o.held, o.heldAt, time.Now())
	held := pending || answered
	if held {
		o.ended = true
	} else {
		o.held, o.heldAt, o.ended = "", time.Time{}, false
	}
	s.tasks.mu.Unlock()
	s.saveSendsOut(ctx)
	return held
}

// sendsOutRecord is one agent's sends of more for its accepted task as kept (sendsOutKey), with the turn held for them.
type sendsOutRecord struct {
	Machine string          `json:"machine"`
	Session string          `json:"session"`
	Sends   []sendOutRecord `json:"sends"`
	Held    string          `json:"held,omitempty"`
	HeldAt  time.Time       `json:"held_at,omitzero"`
	Ended   bool            `json:"ended,omitempty"`
}

// sendOutRecord is one send as kept: the task and the acceptance it asks more of, who sent it and when, and until when
// it counts.
type sendOutRecord struct {
	Task   string    `json:"task"`
	DoneAt time.Time `json:"done_at"`
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
}

// loadSendsOut reads, once, the sends of more the manager before this one kept (sendsOutKey) into memory, before
// anything here reads or writes them. Their answers are lost with that manager, so each counts as answered unknown until
// it stops counting: a turn of its agent that starts by then opens its task (closedTurnReopens). A turn held for them
// is taken up when the manager starts (recoverSendsOut). A read that fails is tried again next time; until one
// succeeds nothing is written (saveSendsOut), so what was kept is not lost.
func (s *Service) loadSendsOut(ctx context.Context) error {
	s.tasks.sendsSaving.Lock()
	defer s.tasks.sendsSaving.Unlock()
	if s.tasks.sendsLoaded {
		return nil
	}
	st := s.store()
	if st == nil {
		s.tasks.sendsLoaded = true
		return nil
	}
	var list []sendsOutRecord
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second) // not a short send's own wait
	defer cancel()
	if _, err := st.Setting(rctx, sendsOutKey, &list); err != nil {
		s.logf("sends of more for accepted tasks: read: %v", err)
		return fmt.Errorf("read the sends kept: %w", err)
	}
	now, taken := time.Now(), 0
	s.tasks.mu.Lock()
	for _, r := range list {
		key := [2]string{r.Machine, r.Session}
		var notes []*agentSendNote
		for _, e := range r.Sends {
			if e.Task != "" && !e.DoneAt.IsZero() && now.Before(e.Until) {
				notes = append(notes, &agentSendNote{s: s, key: key, from: e.From, at: e.At, task: e.Task, doneAt: e.DoneAt,
					until: e.Until, answered: true})
			}
		}
		if r.Machine == "" || r.Session == "" || len(notes) == 0 {
			continue
		}
		o := s.tasks.sendsOut[key]
		if o == nil {
			o = &sendsOut{}
			s.tasks.sendsOut[key] = o
		}
		o.notes = append(o.notes, notes...)
		if r.Held != "" && o.held == "" {
			o.held, o.heldAt, o.ended = r.Held, r.HeldAt, r.Ended
		}
		taken += len(notes)
	}
	s.tasks.mu.Unlock()
	s.tasks.sendsLoaded = true
	if taken > 0 {
		s.logf("sends of more for accepted tasks: %d still out when the manager before this one stopped: answers unknown", taken)
	}
	return nil
}

// saveSendsOut writes the sends of more that still count as they are now, and forgets the others. Writes are ordered
// (sendsSaving), so the latest state is the one kept. Without a store they live in memory only. The error says what is
// in memory was not kept (only noteAgentSend acts on it). s.tasks.mu must not be held.
func (s *Service) saveSendsOut(ctx context.Context) error {
	st := s.store()
	if st == nil {
		return nil
	}
	s.tasks.sendsSaving.Lock()
	defer s.tasks.sendsSaving.Unlock()
	if !s.tasks.sendsLoaded {
		return errors.New("the sends kept before are not read yet") // this write would drop them
	}
	now, list := time.Now(), []sendsOutRecord{}
	s.tasks.mu.Lock()
	for key, o := range s.tasks.sendsOut {
		o.notes = slices.DeleteFunc(o.notes, func(n *agentSendNote) bool { return !now.Before(n.until) })
		if len(o.notes) == 0 {
			delete(s.tasks.sendsOut, key) // a turn held for them is let go (endHeld)
			continue
		}
		r := sendsOutRecord{Machine: key[0], Session: key[1], Held: o.held, HeldAt: o.heldAt.UTC(), Ended: o.ended}
		for _, n := range o.notes {
			r.Sends = append(r.Sends, sendOutRecord{Task: n.task, DoneAt: n.doneAt.UTC(), From: n.from, At: n.at.UTC(), Until: n.until.UTC()})
		}
		list = append(list, r)
	}
	s.tasks.mu.Unlock()
	slices.SortFunc(list, func(a, b sendsOutRecord) int {
		return strings.Compare(a.Machine+"/"+a.Session, b.Machine+"/"+b.Session)
	})
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := st.PutSetting(wctx, sendsOutKey, list); err != nil {
		s.logf("sends of more for accepted tasks: write: %v", err)
		return fmt.Errorf("write the sends: %w", err)
	}
	return nil
}

// recoverSendsOut takes up, as the manager starts (Run), a turn the manager before held for a send whose answer it
// never got (loadSendsOut). That answer is unknown, and a turn held for an unknown answer is the task's
// (sendAnswered): the task opens again unless it moved meanwhile, the turn is followed, and its end is looked at, since
// it may have come while no manager listened. A turn held for sends that stopped counting meanwhile is let go.
func (s *Service) recoverSendsOut(ctx context.Context) {
	_ = s.loadSendsOut(ctx) // a read that failed is tried again by the next send of more
	type heldTurn struct {
		key      [2]string
		id, from string
		doneAt   time.Time
	}
	var turns []heldTurn
	now := time.Now()
	s.tasks.mu.Lock()
	for key, o := range s.tasks.sendsOut {
		if o.held == "" {
			continue
		}
		pending, answered := o.asking(o.held, o.heldAt, now)
		if pending || !answered {
			continue // a send of this manager's decides it, or nothing counts any more (saveSendsOut lets it go)
		}
		h := heldTurn{key: key, id: o.held, doneAt: o.heldAt}
		for _, n := range o.notes {
			if n.counts(o.held, o.heldAt, now) {
				h.from = n.from
			}
		}
		turns = append(turns, h)
	}
	s.tasks.mu.Unlock()
	for _, h := range turns {
		s.logf("task %s: its agent %s/%s started a turn while more for it was being sent, and the manager before this one stopped before the answer: the task's",
			h.id, h.key[0], short(h.key[1]))
		s.tasks.completionMu.Lock()
		s.sendsSettle(ctx, h.key, h.id, h.doneAt, h.from, true, true)
		s.tasks.completionMu.Unlock()
	}
	if len(turns) == 0 {
		s.saveSendsOut(ctx) // what stopped counting meanwhile is forgotten
	}
}
