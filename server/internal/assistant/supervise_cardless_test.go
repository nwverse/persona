package assistant

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A finished, supervised task whose supervisor asked the owner to review or decide only in chat — with no
// needs_user conclusion, user_check ask, ask_user card or pending worker interaction — must not stay silent: after
// the settle time the watch hands it back to its supervisor once, naming the card to open (#12648).
func TestReviewCardlessWaitGoesBackOnce(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了，你看下要不要这样发布。")
	if got := todoNow(s, todo.ID).Supervision.State; got != reviewPending {
		t.Fatalf("state = %q, want pending_review", got)
	}
	// Inside the settle window: nothing yet.
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle-time.Minute))
	if slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatal("a card-less review was handed back inside the settle window")
	}
	before := len(f.notices())
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Minute))
	if !slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("a card-less review was not handed back: %v", pendingKinds(s, todo.ID))
	}
	cur := todoNow(s, todo.ID)
	if cur.Supervision.CardlessToldAt == nil {
		t.Fatal("the hand-back was not kept on the ledger round")
	}
	releaseDigest(s)
	waitFor(t, "the card-less message", func() bool { return len(f.notices()) > before })
	n := f.notices()[before]
	for _, want := range []string{"signal=cardless_wait", "没有任何卡片", "needs_user"} {
		if !strings.Contains(n, want) {
			t.Errorf("message lacks %q:\n%s", want, n)
		}
	}
	forgetSignals(t, s)
	// The same review round is never handed back again, however many passes pass.
	s.superviseWatch(ctx, time.Now().Add(2*time.Hour))
	s.superviseWatch(ctx, time.Now().Add(48*time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatal("the same review round was handed back twice")
	}
}

// A worker that still holds a card the owner can answer is not card-less.
func TestReviewCardlessWaitSkipsPendingCard(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	// The worker comes back with a question of its own: a real pending interaction stands for the owner.
	agentTurn(s, f, proto.StatusBlocked, "选哪个方案？", 1)
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("a task whose worker has a pending card is not card-less: %v", pendingKinds(s, todo.ID))
	}
}

// An open acceptance/conclusion ask already gives the owner something to answer.
func TestReviewCardlessWaitSkipsOpenAsk(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	// In the fake hub the bot card send fails, but the ask is still kept in the acceptance book (as the undecidable
	// tests rely on): the book is what the backstop reads.
	s.askConclusion(ctx, todoNow(s, todo.ID), conclusionInput{Outcome: outcomeDecide, Reason: "要不要发布", Recommend: "发布"})
	if a := askOf(s, todo.ID); a == nil {
		t.Fatal("the conclusion ask was not kept")
	}
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("a task with an open ask is not card-less: %v", pendingKinds(s, todo.ID))
	}
}

// A new review round clears the round marker, so a card-less wait in the new round is told again.
func TestReviewCardlessWaitRearmsOnNewRound(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Minute))
	if !slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("first round not handed back: %v", pendingKinds(s, todo.ID))
	}
	// A rework leaves pending_review; the next done judgment starts a fresh review round.
	if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": reviewRework, "note": "补上错误处理"}); err != nil {
		t.Fatal(err)
	}
	if cur := todoNow(s, todo.ID); cur.Supervision.CardlessToldAt != nil || cur.Supervision.State != reviewRework {
		t.Fatalf("the marker was not cleared when the round ended: %+v", cur.Supervision)
	}
	forgetSignals(t, s)
	holdDigest(s)
	waitsForReview(t, s, f, todo.ID, "补上了，再看。")
	cur := todoNow(s, todo.ID)
	if cur.Supervision.State != reviewPending || cur.Supervision.CardlessToldAt != nil {
		t.Fatalf("the new round did not start clean: %+v", cur.Supervision)
	}
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Minute))
	if !slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("the new card-less round was not handed back: %v", pendingKinds(s, todo.ID))
	}
}

// The round marker survives a manager restart on a Postgres-backed service, so the same round is not told twice.
func TestReviewCardlessWaitSurvivesRestart(t *testing.T) {
	database := taskRecoveryDatabase(t)
	s, f, todo := newSupervisedServiceOn(t, database)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Minute))
	if !slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("card-less review not handed back: %v", pendingKinds(s, todo.ID))
	}
	forgetSignals(t, s)
	again, _ := restartedOn(t, database)
	holdDigest(again)
	if err := again.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	cur := todoNow(again, todo.ID)
	if cur.Supervision.CardlessToldAt == nil || cur.Supervision.State != reviewPending {
		t.Fatalf("the marker did not survive the restart: %+v", cur.Supervision)
	}
	again.superviseWatch(ctx, time.Now().Add(2*time.Hour))
	if slices.Contains(pendingKinds(again, todo.ID), sigCardlessWait) {
		t.Fatal("a restart reissued the same card-less hand-back")
	}
}

// cardlessWorkersStopped judges only the sessions it is given: a worker still working or starting keeps the review
// live, a worker holding a card stands for the owner, and only fully stopped, card-less sessions count as settled.
func TestCardlessWorkersStopped(t *testing.T) {
	stopped := proto.Session{SessionID: "s1", State: "idle"}
	if !cardlessWorkersStopped([]proto.Session{stopped}) {
		t.Error("an idle, card-less worker should count as stopped")
	}
	if cardlessWorkersStopped([]proto.Session{stopped, {SessionID: "s2", State: proto.StateWorking}}) {
		t.Error("a still-working worker should keep the review live")
	}
	if cardlessWorkersStopped([]proto.Session{stopped, {SessionID: "s3", Starting: true}}) {
		t.Error("a starting worker should keep the review live")
	}
	if cardlessWorkersStopped([]proto.Session{stopped, {SessionID: "s4", State: proto.StateNeedsYou}}) {
		t.Error("a worker holding an owner card means the review is not card-less")
	}
	if cardlessWorkersStopped([]proto.Session{stopped, {SessionID: "s5", PendingInteractions: 1}}) {
		t.Error("a worker with a pending interaction means the review is not card-less")
	}
}

// pendingCardlessSignal returns the queued card-less signal for id, failing the test if it is not there.
func pendingCardlessSignal(t *testing.T, s *Service, id string) superviseSignal {
	t.Helper()
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.pending[id+"|"+sigCardlessWait]
	if !ok {
		t.Fatalf("no pending cardless signal for %s", id)
	}
	return g
}

// A machine whose session list fails is not a stopped worker: the hand-back must wait for a reachable pass rather
// than spend the round's one notification (and its CardlessToldAt stamp) on an unknown state (#12648 P1).
func TestReviewCardlessWaitWaitsForAReachableWorker(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	f.sessionsFail["box"] = 100
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Hour))
	if slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatal("the hand-back fired while the worker's machine was unreachable")
	}
	if todoNow(s, todo.ID).Supervision.CardlessToldAt != nil {
		t.Fatal("the round marker was stamped on an unreachable worker")
	}
	// Once the machine answers again, the same review round is handed back normally.
	f.sessionsFail["box"] = 0
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+2*time.Hour))
	if !slices.Contains(pendingKinds(s, todo.ID), sigCardlessWait) {
		t.Fatalf("the hand-back did not fire after the machine recovered: %v", pendingKinds(s, todo.ID))
	}
}

// A digest entry whose conditions changed while it queued is dropped: if an owner card (acceptance ask) appears after
// the marker was stamped, the stale "no card" entry must not render (#12648 P1).
func TestCardlessDigestDropsWhenACardAppears(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Minute))
	g := pendingCardlessSignal(t, s, todo.ID)
	if _, ok := s.digestEntry(ctx, g, ""); !ok {
		t.Fatal("the card-less entry did not render while still card-less")
	}
	// An owner-answerable ask now stands; the same queued entry must be suppressed on the next render.
	s.askConclusion(ctx, todoNow(s, todo.ID), conclusionInput{Outcome: outcomeDecide, Reason: "要不要发布", Recommend: "发布"})
	if askOf(s, todo.ID) == nil {
		t.Fatal("the conclusion ask was not kept")
	}
	if _, ok := s.digestEntry(ctx, g, ""); ok {
		t.Fatal("the stale card-less entry rendered after an owner card appeared")
	}
}

// A worker that goes back to work after the marker is stamped also invalidates the queued card-less entry. The
// session list is set directly so the ledger still reads pending_review (the snapshot window before
// supervisedWorking moves it): this exercises the digest's own worker revalidation rather than the state hold.
func TestCardlessDigestDropsWhenAWorkerResumes(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	waitsForReview(t, s, f, todo.ID, "做完了。")
	s.superviseWatch(ctx, time.Now().Add(cardlessSettle+time.Minute))
	g := pendingCardlessSignal(t, s, todo.ID)
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
	if cur := todoNow(s, todo.ID).Supervision; cur.state(todoNow(s, todo.ID)) != reviewPending {
		t.Fatalf("the direct session edit must leave the ledger in pending_review: %+v", cur)
	}
	if _, ok := s.digestEntry(ctx, g, ""); ok {
		t.Fatal("the stale card-less entry rendered after its worker resumed")
	}
}

// A task leader's supervision message is rendered from worker_supervise.md, not the assistant supervise.md template:
// the cardless_wait entry must carry an explanation and the required review action there too, instead of rendering as
// an unexplained numbered line (#12648 P2).
func TestCardlessWaitRenderedForTaskLeader(t *testing.T) {
	e := prompts.SuperviseEntry{ID: "todo_1", Title: "Fix the build", Kind: sigCardlessWait, Minutes: 12}
	got := prompts.RenderWorkerSupervise([]prompts.SuperviseEntry{e})
	for _, want := range []string{"12 minutes for your review", "no card anyone can answer", "review it now", "Fix the build"} {
		if !strings.Contains(got, want) {
			t.Errorf("worker cardless_wait render lacks %q:\n%s", want, got)
		}
	}
}
