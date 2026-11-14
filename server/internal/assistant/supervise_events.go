package assistant

import (
	"cmp"
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// ---- what the task's events say ----

// taskNotice is what happened to task t (state: done, needs, interrupted, failed; ask, text as quietNotify has them):
// a supervised task's supervisor gets a signal, any other quiet task's conversation its notice.
func (s *Service) taskNotice(ctx context.Context, t Todo, machine, state, ask, text string) {
	// A reporting conversation keeps its own quiet notice even though main now supervises the task (#5732). Main's own
	// quiet tasks use only the supervision digest, so they are not told twice.
	if t.Quiet && t.Origin != "" {
		if _, reporting := s.reporterOf(ctx, t.Origin); reporting {
			if s.reporterStopped(ctx, t.Origin) {
				return
			}
			s.quietNotify(ctx, t, machine, state, ask, text)
		}
	}
	if t.Supervision == nil {
		if !t.Quiet {
			if state == "failed" && t.Origin != "" {
				s.notifyTaskFailed(ctx, t, machine, text)
			}
			return
		}
		s.quietNotify(ctx, t, machine, state, ask, text)
		return
	}
	if state == "failed" && !t.Quiet && t.Origin != "" && !s.isMain(ctx, t.Origin) && !s.isLeader(ctx, t.Origin) &&
		!s.isLeaderRef(t.Supervision.Supervisor) {
		// A side conversation's task, whoever supervises it now: the user still gets the failure push they got before
		// the task had a ledger (#5081), and the supervisor its signal. A leader conversation's own task, and a
		// worker's, stay their supervisor's business.
		s.notifyTaskFailed(ctx, t, machine, text)
	}
	g := superviseSignal{Todo: t.ID, Machine: machine, Detail: text, At: time.Now()}
	switch state {
	case "failed":
		g.Kind, g.Key = sigFailed, text
	case "interrupted":
		g.Kind, g.Key = sigInterrupted, time.Now().UTC().Format(time.RFC3339)
	case "needs":
		b := &s.supervise
		b.mu.Lock()
		b.init()
		b.asks[t.ID] = ask
		b.mu.Unlock()
		if !t.Quiet {
			return // the user was pushed: the supervisor hears of it if it waits (watchSignals)
		}
		g.Kind, g.Key, g.Ask, g.Detail = sigNeedsYou, "ask@"+time.Now().UTC().Format(time.RFC3339), ask, ""
	default:
		return // done: the judge's verdict says what follows (superviseJudged)
	}
	s.signal(g)
}

// superviseJudged: supervised task t's agents all stopped after work since work, and the judge said c (err: it could
// not). Finished: the task waits for review. Not finished: the supervisor hears it stopped short.
func (s *Service) superviseJudged(ctx context.Context, t Todo, work time.Time, c taskDoneCheck, err error) {
	st := t.Supervision.state(t)
	key := work.UTC().Format(time.RFC3339Nano)
	if err == nil && c.Verdict == verdictDone {
		if s.autoReviewBegin(ctx, t, work, c.Reason) {
			return // the independent review first; it ends in rework or the wait for review (supervise_autoreview.go)
		}
		detail := c.Reason
		v, _, uerr := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if v.state(t) == reviewPending && v.Waits == nil {
				return false
			}
			v.State, v.Waits = reviewPending, nil
			return true
		})
		if uerr != nil {
			s.logf("task %s: pending review: %v", t.ID, uerr)
			return
		}
		s.logf("task %s: waits for review: %s", t.ID, detail)
		s.signal(superviseSignal{Todo: t.ID, Kind: sigReview, Key: reviewKey(&v), Detail: detail})
		return
	}
	if st == reviewNeedsUser || st == reviewParked {
		return // the supervisor knows it waits: for the user, or parked until its time (parked.go)
	}
	if v := t.Supervision; t.DoneAt == nil && (v.State == reviewAccepted || v.State == reviewCancelled) {
		// Closed by its review, open again only because its agent was sent more (taskWorking, #5892): what it did then
		// is no task stopped short. Done, it waits for review again (above); else the supervisor is not told (#3111).
		s.logf("task %s: %s before, more work since judged %s: not told", t.ID, v.State, cmp.Or(c.Verdict, "with an error"))
		return
	}
	if c.Verdict == verdictNeedsUser {
		// An explicit owner action is deterministic evidence, even when the model call carrying the rest of the
		// judgment failed (#5732). It is not an unjudged stop and must still reach the owner.
		s.noteOwnerWait(ctx, t, work, c.Reason)
		return
	}
	if err != nil {
		// The judge failed (the model service, a reply that was no verdict): no word on whether it is finished, not
		// "stopped short" (#3111). The supervisor reads the agent's reply and decides.
		s.signal(superviseSignal{Todo: t.ID, Kind: sigUnjudged, Key: key, Detail: "判定模型出错"})
		return
	}
	detail := verdictWords[c.Verdict]
	if c.Reason != "" {
		detail += "：" + c.Reason
	}
	if c.Verdict == verdictUnsure { // no word on it either way: not "stopped short" (#3111)
		s.signal(superviseSignal{Todo: t.ID, Kind: sigUnjudged, Key: key, Detail: detail})
		return
	}
	if d := c.Delivery; c.Verdict == verdictUndelivered && d.held() { // where it is held, to chase (delivery.go)
		detail += "（" + deliveryLabels[d.State]
		if d.Gate != "" {
			detail += "，卡在「" + d.Gate + "」"
		}
		detail += "）"
	}
	s.signal(superviseSignal{Todo: t.ID, Kind: sigIdle, Key: key, Detail: detail})
}

var verdictWords = map[string]string{verdictNotDone: "没做完", verdictWaiting: "在等别人或别的事", verdictNeedsUser: "要用户决定",
	verdictUnsure: "看不出做没做完", verdictUndelivered: "做完了但还没上线"}

// ownerPushTimeout bounds one delivery and its receipt, so a stalled hub cannot hold up every other task's owner push.
var ownerPushTimeout = 10 * time.Second

// noteOwnerWait makes the owner's action the task's visible state and tells the supervisor at once. A stretch pushes
// once after delivery; an undelivered push may retry when its conversation route recovers (#5732).
func (s *Service) noteOwnerWait(ctx context.Context, t Todo, work time.Time, reason string) {
	v, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.Waits != nil && v.Waits.Work.Equal(work) && v.State == reviewNeedsUser {
			return false
		}
		v.Waits = &OwnerWait{Work: work.UTC(), At: time.Now().UTC(), Reason: clip(oneLine(reason), 200), PushPending: true}
		v.State = reviewNeedsUser
		return true
	})
	if err != nil {
		s.logf("task %s: owner wait: %v", t.ID, err)
		return
	}
	s.signal(superviseSignal{Todo: t.ID, Kind: sigIdle, Key: work.UTC().Format(time.RFC3339Nano), Detail: "要用户决定：" + reason}) // i18n-ignore: the supervision message is the assistant's prompt
	if changed || (v.Waits != nil && v.Waits.PushPending) {
		// Quiet suppresses routine notices, not an action only the owner can take. An undelivered wait (for example,
		// its conversation was not configured yet) may retry; a delivered one has PushPending cleared.
		s.startOwnerWaitDelivery(ctx, t, work, reason)
	}
}

// startOwnerWaitDelivery gives one push and its durable receipt an independent deadline even when its caller is a
// fire-and-forget settings or judgment path. WithoutCancel only detaches cancellation; it must not remove the bound.
func (s *Service) startOwnerWaitDelivery(ctx context.Context, t Todo, work time.Time, reason string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ownerPushTimeout)
	go func() {
		defer cancel()
		s.deliverOwnerWait(ctx, t, work, reason)
	}()
}

// retryOwnerWaits reconciles explicitly undelivered pushes when a conversation route is restored or todos finish
// loading. Legacy waits have no PushPending and are never replayed on upgrade; delivered waits have it cleared.
func (s *Service) retryOwnerWaits(ctx context.Context) {
	if !s.todosLoaded() {
		return
	}
	for _, t := range s.todosOf(ctx, "*") {
		if t.DoneAt != nil || t.Supervision == nil || t.Supervision.State != reviewNeedsUser {
			continue
		}
		w := t.Supervision.Waits
		if w != nil && w.PushPending && w.NotifiedAt == nil {
			s.startOwnerWaitDelivery(ctx, t, w.Work, w.Reason)
		}
	}
}

// deliverOwnerWait claims one todo/work push while it and its durable receipt are in flight. The claim lock covers
// only the map: an uninterruptible hub writer or a receipt waiting on the ledger cannot block another task (#5732).
func (s *Service) deliverOwnerWait(ctx context.Context, t Todo, work time.Time, reason string) {
	b := &s.supervise
	key := t.ID + "\x00" + work.UTC().Format(time.RFC3339Nano)
	b.ownerPushMu.Lock()
	if b.ownerPushes == nil {
		b.ownerPushes = map[string]struct{}{}
	}
	if _, flying := b.ownerPushes[key]; flying {
		b.ownerPushMu.Unlock()
		return
	}
	b.ownerPushes[key] = struct{}{}
	b.ownerPushMu.Unlock()
	defer func() {
		b.ownerPushMu.Lock()
		delete(b.ownerPushes, key)
		b.ownerPushMu.Unlock()
	}()
	current, ok := s.todoNow(t.ID)
	if !ok || current.Supervision == nil || current.Supervision.Waits == nil || current.Supervision.State != reviewNeedsUser ||
		!current.Supervision.Waits.Work.Equal(work) || !current.Supervision.Waits.PushPending || current.Supervision.Waits.NotifiedAt != nil {
		return
	}
	if !s.notifyTaskNeedsUser(ctx, current, work, reason) {
		return
	}
	if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.Waits == nil || v.State != reviewNeedsUser || !v.Waits.Work.Equal(work) || !v.Waits.PushPending || v.Waits.NotifiedAt != nil {
			return false
		}
		at := time.Now().UTC()
		v.Waits.NotifiedAt = &at
		v.Waits.PushPending = false
		return true
	}); err != nil {
		// The stable notification id still deduplicates a later retry at the real hub if this receipt cannot be saved.
		s.logf("task %s: owner-wait push receipt: %v", t.ID, err)
	}
}

// notifyTaskNeedsUser pushes an action only the owner can take. Its stable id deduplicates delivery retries, and its
// body names the reason and the task's web link. Quiet tasks receive this push too; their routine notices stay quiet.
func (s *Service) notifyTaskNeedsUser(ctx context.Context, t Todo, work time.Time, reason string) bool {
	cfg := s.settings(ctx)
	here := cfg.MachineID
	conversation := t.Origin
	if t.Supervision != nil {
		if cfg.Main != "" {
			// Owner decisions escalate to main from a manual leader, a task-leader Agent and the legacy background too.
			conversation = cfg.Main
		} else if supervisor := t.Supervision.Supervisor; supervisor != "" {
			if _, _, worker := parseLeaderRef(supervisor); !worker {
				conversation = supervisor
			}
		}
	}
	_, _, worker := parseLeaderRef(conversation)
	if conversation == "" || worker || s.muted(here, conversation) {
		return false // a machine/session worker reference is never an assistant conversation id
	}
	l := s.lang(ctx)
	title := i18n.T(l, "assistant-tasks.task_needs_user_title", i18n.Args{"name": t.Title})
	if s.inHealth(ctx, t.Origin) {
		title = i18n.T(l, "assistant-tasks.task_needs_user_title_health")
	}
	body := clip(strings.TrimSpace(reason), 500)
	if link := todoLink(s.webBase(ctx), t.ID); link != "" {
		body = strings.TrimSpace(body + "\n" + link)
	}
	n := Notification{ID: "ntf_task_needs_" + t.ID + "_" + work.UTC().Format(time.RFC3339Nano), Kind: NotifyPush,
		Subtitle: "Persona", DeviceID: here, SessionID: conversation, Title: title, Body: body}
	if err := s.hub.NotifyRequest(ctx, NotifyRequest{Notification: n}); err != nil {
		s.logf("task %s: needs-user push: %v", t.ID, err)
		return false
	}
	return true
}

// supervisedWorking: one of supervised task t's agents started a turn: a review waiting is moot (the work goes on),
// and a parked task is parked no longer (someone sent its agent something: it is supervised as before, parked.go).
func (s *Service) supervisedWorking(ctx context.Context, t Todo) {
	s.autoReviewWorking(ctx, t)
	if st := t.Supervision.state(t); st != reviewPending && st != reviewParked && (t.Supervision == nil || t.Supervision.Waits == nil) {
		return
	}
	if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.State != reviewPending && v.State != reviewParked && v.Waits == nil {
			return false
		}
		v.Waits = nil // its agent goes on: the owner's answer reached it, or the work changed (#1277)
		if v.State != reviewPending && v.State != reviewParked && v.State != reviewNeedsUser {
			return true
		}
		// A judge-written owner wait ends with this work too. A needs_user review/decision card has no Waits and was
		// returned above, so it is not cleared here (#5732).
		v.State = ""
		if v.Round > 0 {
			v.State = reviewRework
		}
		return true
	}); err != nil {
		s.logf("task %s: supervision: %v", t.ID, err)
	}
	b := &s.supervise
	b.mu.Lock()
	delete(b.pending, t.ID+"|"+sigReview)
	delete(b.pending, t.ID+"|"+sigParkedDue)
	s.forgetSupervisePending(t.ID+"|"+sigReview, t.ID+"|"+sigParkedDue)
	b.mu.Unlock()
}

// notifyTaskFailed pushes a failure notification to the user's phone when a task started from a side conversation
// (neither supervised nor quiet) could not start, so the user is informed with next steps (#5081).
// Muted conversations send nothing; health conversations omit the task title.
func (s *Service) notifyTaskFailed(ctx context.Context, t Todo, machine, reason string) {
	here := s.settings(ctx).MachineID
	if here == "" {
		here = machine
	}
	if t.Origin == "" || s.muted(here, t.Origin) {
		return
	}
	l := s.lang(ctx)
	title := i18n.T(l, "assistant-tasks.task_failed_title", i18n.Args{"name": t.Title})
	if s.inHealth(ctx, t.Origin) {
		title = i18n.T(l, "assistant-tasks.task_failed_title_health")
	}
	r := strings.TrimRight(strings.TrimSpace(reason), ".。") // i18n-ignore: trailing full stops trimmed, not text
	body := i18n.T(l, "assistant-tasks.task_failed_body_no_reason")
	if r != "" {
		body = i18n.T(l, "assistant-tasks.task_failed_body", i18n.Args{"reason": r})
	}
	sub := s.machineName(machine)
	if sub == "" {
		sub = "Persona"
	}
	s.push(ctx, Notification{
		Subtitle:  sub,
		DeviceID:  here,
		SessionID: t.Origin,
		Title:     title,
		Body:      body,
	})
}
