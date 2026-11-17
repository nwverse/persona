package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const externalPRSource = "https://github.com/example/app/pull/12"

func externalPRParkArgs(source string) map[string]any {
	return map[string]any{"verdict": "parked", "reason": "Waiting for the recorded external object",
		"waiting_on": parkExternalEvent, "until": "2d",
		"evidence": testParkEvidence("external_event", "the upstream change", source, "The PR becomes terminal; reassess the dependency")}
}

type externalPRForge struct {
	fakeForge
	terminal string
	calls    []string
	before   func()
}

func (f *externalPRForge) PRStatus(_ context.Context, repo string, number int) (TodoPRStatus, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s#%d", repo, number))
	if f.before != nil {
		before := f.before
		f.before = nil
		before()
	}
	if repo == "example/app" && number == 12 {
		return TodoPRStatus{State: f.terminal}, nil
	}
	return TodoPRStatus{State: "open", HeadOID: "other-head"}, nil
}

func externalPRReceipt(t *testing.T, task Todo) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(task.Supervision.Reviews)
	var reviews []map[string]any
	if err := json.Unmarshal(raw, &reviews); err != nil || len(reviews) == 0 {
		t.Fatalf("reviews: %s %v", raw, err)
	}
	receipt, _ := reviews[len(reviews)-1]["external_pr"].(map[string]any)
	return receipt
}

// Existing entrypoints/raw JSON keep these compiling when the added production implementation is removed.
func TestExternalPRParkWakesFromItsBoundObject(t *testing.T) {
	for _, association := range []string{"current", "earlier", "no association"} {
		for _, state := range []string{"merged", "closed"} {
			t.Run(association+"/"+state, func(t *testing.T) {
				s, _, task := newSupervisedService(t)
				holdDigest(s)
				ctx := context.Background()
				if association != "no association" {
					s.setTaskStage(ctx, task.ID, "review", externalPRSource)
				}
				if association == "earlier" {
					s.setTaskStage(ctx, task.ID, "review", "https://github.com/example/app/pull/99")
				}
				args := externalPRParkArgs("https://GitHub.com/Example/App/pull/12/?tab=checks#discussion")
				if _, err := reviewErr(s, task.ID, args); err != nil {
					t.Fatal(err)
				}
				before := todoNow(s, task.ID)
				forge := &externalPRForge{terminal: state}
				s.deploy.forge = forge
				now := time.Now()
				s.sweepPRStatuses(ctx, now)
				got := todoNow(s, task.ID)
				if got.Supervision.State != reviewPending || !pendingReviewSignal(s, task.ID) {
					t.Fatalf("explicit external object did not wake: state=%q queued=%v reads=%v",
						got.Supervision.State, pendingKinds(s, task.ID), forge.calls)
				}
				receipt := externalPRReceipt(t, got)
				if receipt["url"] != externalPRSource || receipt["state"] != state || receipt["checked_at"] == nil {
					t.Fatalf("source receipt missing: %v", receipt)
				}
				if got.DoneAt != nil || got.PRURL != before.PRURL || !reflect.DeepEqual(got.PRs, before.PRs) ||
					len(got.Supervision.Reviews) != len(before.Supervision.Reviews) ||
					!got.Supervision.Reviews[0].At.Equal(before.Supervision.Reviews[0].At) {
					t.Fatal("observation completed the task, rewrote its park history, or fabricated a task PR association")
				}
				if association == "earlier" && !slices.Contains(forge.calls, "example/app#99") {
					t.Fatal("the independent open PR was displaced by the bound external source")
				}
				s.sweepPRStatuses(ctx, now.Add(deployRecheck))
				if !slices.Equal(pendingKinds(s, task.ID), []string{sigReview}) {
					t.Fatal("terminal replay introduced another logical wake")
				}
			})
		}
	}
}

func TestExternalPRParkIgnoresNonObjectSources(t *testing.T) {
	for _, source := range []string{
		"https://example.org/releases/next", "https://github.com/example/app/issues/12",
		"https://github.com/example/app/pull/12/files", "https://github.com.example.org/example/app/pull/12",
		"https://git.example.org/example/app/-/merge_requests/12",
	} {
		t.Run(source, func(t *testing.T) {
			s, _, task := newSupervisedService(t)
			holdDigest(s)
			if _, err := reviewErr(s, task.ID, externalPRParkArgs(source)); err != nil {
				t.Fatal(err)
			}
			forge := &externalPRForge{terminal: "merged"}
			s.deploy.forge = forge
			s.sweepPRStatuses(context.Background(), time.Now())
			if todoNow(s, task.ID).Supervision.State != reviewParked || len(forge.calls) != 0 || pendingReviewSignal(s, task.ID) {
				t.Fatal("a different external object was treated as a GitHub PR")
			}
		})
	}
}

func TestExternalPRParkRechecksCapturedWait(t *testing.T) {
	for _, change := range []string{"repark", "replace", "complete", "cancel"} {
		t.Run(change, func(t *testing.T) {
			s, _, task := newSupervisedService(t)
			holdDigest(s)
			if _, err := reviewErr(s, task.ID, externalPRParkArgs(externalPRSource)); err != nil {
				t.Fatal(err)
			}
			forge := &externalPRForge{terminal: "merged", before: func() {
				switch change {
				case "complete":
					w := httptest.NewRecorder()
					s.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/v1/todos/"+task.ID, strings.NewReader(`{"done":true}`)))
					if w.Code != http.StatusOK {
						t.Errorf("complete: %d %s", w.Code, w.Body.String())
					}
				case "cancel":
					if _, err := reviewErr(s, task.ID, map[string]any{"verdict": "cancelled", "note": "No longer needed"}); err != nil {
						t.Error(err)
					}
				default:
					source := externalPRSource
					if change == "replace" {
						source = "https://github.com/example/app/pull/98"
					}
					if _, err := reviewErr(s, task.ID, externalPRParkArgs(source)); err != nil {
						t.Error(err)
					}
				}
			}}
			s.deploy.forge = forge
			s.sweepPRStatuses(context.Background(), time.Now())
			if len(forge.calls) != 1 {
				t.Fatal("the source read did not run, so the in-flight fence was not exercised")
			}
			if todoNow(s, task.ID).Supervision.State == reviewPending || pendingReviewSignal(s, task.ID) {
				t.Fatal("an in-flight response woke a replaced or closed wait")
			}
		})
	}
}

func TestExternalPRParkRejectsOlderReceiptAndKeepsPollInterval(t *testing.T) {
	s, _, task := newSupervisedService(t)
	holdDigest(s)
	if _, err := reviewErr(s, task.ID, externalPRParkArgs(externalPRSource)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.updateSupervision(context.Background(), task.ID, func(v *Supervision) bool {
		v.Reviews[len(v.Reviews)-1].At = time.Now().Add(-time.Minute)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	forge := &externalPRForge{terminal: "open"}
	s.deploy.forge = forge
	now := time.Now()
	s.sweepPRStatuses(context.Background(), now)
	cur := todoNow(s, task.ID)
	if receipt := externalPRReceipt(t, cur); receipt["state"] != "open" || pendingReviewSignal(s, task.ID) {
		t.Fatalf("open source not durably observed, or woke early: %v", receipt)
	}
	if err := s.applyPRStatus(context.Background(), cur,
		TodoPRStatus{URL: externalPRSource, State: "merged", CheckedAt: now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	s.sweepPRStatuses(context.Background(), now.Add(time.Minute))
	if len(forge.calls) != 1 || todoNow(s, task.ID).Supervision.State != reviewParked {
		t.Fatal("an old receipt or unthrottled source woke the task")
	}
	forge.terminal = "merged"
	s.sweepPRStatuses(context.Background(), now.Add(deployRecheck))
	if len(forge.calls) != 2 || !pendingReviewSignal(s, task.ID) {
		t.Fatal("the terminal source was not reconsidered after the normal interval")
	}
}

func TestExternalPRParkReceiptAndReviewRecoverTogether(t *testing.T) {
	ctx := context.Background()
	store := &ledgerDown{DB: taskRecoveryDatabase(t)}
	s, _, task := newSupervisedServiceOn(t, store)
	holdDigest(s)
	if _, err := reviewErr(s, task.ID, externalPRParkArgs(externalPRSource)); err != nil {
		t.Fatal(err)
	}
	before := todoNow(s, task.ID)
	old, _ := json.Marshal(before.Supervision)
	forge := &externalPRForge{terminal: "merged"}
	s.deploy.forge = forge
	store.down.Store(true)
	now := time.Now()
	s.sweepPRStatuses(ctx, now)
	after, _ := json.Marshal(todoNow(s, task.ID).Supervision)
	if string(old) != string(after) || pendingReviewSignal(s, task.ID) || len(forge.calls) != 1 {
		t.Fatal("failed ledger write published a receipt/review, or source was not read")
	}
	store.down.Store(false)
	again, _ := restartedOn(t, store)
	holdDigest(again)
	again.deploy.forge = &externalPRForge{terminal: "merged"}
	again.sweepPRStatuses(ctx, now.Add(time.Minute))
	got := todoNow(again, task.ID)
	if got.DoneAt != nil || got.Supervision.State != reviewPending || !pendingReviewSignal(again, task.ID) ||
		externalPRReceipt(t, got)["state"] != "merged" {
		t.Fatal("reconstruction did not retry and commit the same external source")
	}
	third, _ := restartedOn(t, store)
	holdDigest(third)
	if err := third.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	loaded := todoNow(third, task.ID)
	if loaded.Supervision.State != reviewPending || externalPRReceipt(t, loaded)["state"] != "merged" {
		t.Fatal("receipt and review did not survive another reconstruction")
	}
	signals := watchSignals(loaded, nil, now.Add(2*time.Minute))
	if len(signals) != 1 || signals[0].Kind != sigReview {
		t.Fatalf("lost in-memory wake was not recoverable from the persisted review: %v", signals)
	}
}
