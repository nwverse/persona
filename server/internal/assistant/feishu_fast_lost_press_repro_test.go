package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/feishubot"
)

// Another client wins while resolve is in flight. An ended event on either side
// of the fast result must leave no live buttons or second answer delivery.
func TestReproFastLostPressClosesCard(t *testing.T) {
	for _, during := range []bool{true, false} {
		name := "ended after callback"
		if during {
			name = "ended during resolve"
		}
		t.Run(name, func(t *testing.T) {
			ix := qCard("ix_fast_lost", false, "Beijing", "Shanghai")
			var ended atomic.Bool
			var resolutions atomic.Int64
			s, f, _, _, _ := textAnswerBot(t, nil, ix)
			// The real ended handler reads the hub too, so this resolver runs outside
			// the fake's lock while that event is delivered.
			f.answerAppBlocking(AppInteractions, InteractionsResolve, func(json.RawMessage) (any, error) {
				resolutions.Add(1)
				ended.Store(true)
				if during {
					s.InteractionEnded(context.Background(), InteractionEnded{Interaction: ix})
				}
				return map[string]any{"ok": false, "status": "resolved"}, nil
			})
			endingQueue(f, ix, &ended, "resolved")
			var updates int
			f.answerApp(AppFeishu, FeishuUpdateCard, func(json.RawMessage) (any, error) {
				updates++
				return map[string]any{}, nil
			})
			seedCard(s, ix)
			ctx := context.Background()
			assertClosed := func(r *feishubot.CardResponse) {
				t.Helper()
				if r == nil || r.Toast == nil || r.Toast.Type != "info" || r.Toast.Content != "已经处理过了" || r.Card == nil {
					t.Fatalf("expected handled toast and closed callback card, got %+v", r)
				}
				raw, err := json.Marshal(r.Card)
				if err != nil || strings.Contains(string(raw), `"callback"`) {
					t.Fatalf("card still has actions: %s (%v)", raw, err)
				}
			}
			assertClosed(s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_fake"))
			// Redelivery sees the authoritative ended state and cannot resolve again.
			assertClosed(s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_fake"))
			s.InteractionEnded(context.Background(), InteractionEnded{Interaction: ix})
			s.fbot.mu.Lock()
			_, tracked := s.fbot.cards[ix.ID]
			s.fbot.mu.Unlock()
			f.mu.Lock()
			n, attempts := updates, resolutions.Load()
			f.mu.Unlock()
			if tracked || n != 0 || attempts != 1 {
				t.Fatalf("tracked=%v external updates=%d resolve attempts=%d", tracked, n, attempts)
			}
		})
	}
}

func TestFastQuestionPressAcceptedOrRetryable(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "accepted"
		if fails {
			name = "transport error"
		}
		t.Run(name, func(t *testing.T) {
			ix := qCard("ix_fast_answer", false, "Beijing", "Shanghai")
			s, _, _, _, resolves := textAnswerBot(t, func(json.RawMessage) (any, error) {
				if fails {
					return nil, errors.New("temporary transport failure")
				}
				return map[string]any{"ok": true}, nil
			}, ix)
			seedCard(s, ix)
			r := s.botCardAction(context.Background(), ixPress(ix.ID, "Shanghai"), "oa_fake")
			s.fbot.mu.Lock()
			_, tracked := s.fbot.cards[ix.ID]
			s.fbot.mu.Unlock()
			if r == nil || r.Toast == nil || len(*resolves) != 1 {
				t.Fatalf("response=%+v resolves=%v", r, *resolves)
			}
			if fails {
				if r.Toast.Type != "error" || r.Card != nil || !tracked {
					t.Fatalf("failed answer must remain retryable: response=%+v tracked=%v", r, tracked)
				}
			} else if r.Toast.Type != "success" || r.Card == nil || tracked {
				t.Fatalf("accepted answer must close: response=%+v tracked=%v", r, tracked)
			}
		})
	}
}

// Resolve can lose its live entry while history still says pending, or cannot be
// read at all. Neither response is proof that the displayed question has ended.
func TestNonfinalQuestionPressRemainsRetryable(t *testing.T) {
	defer func(w time.Duration) { cardAnswerWait = w }(cardAnswerWait)
	for _, status := range []string{"pending", ""} {
		for _, late := range []bool{false, true} {
			for _, fails := range []bool{false, true} {
				name := status
				if name == "" {
					name = "unknown"
				}
				if late {
					name += "/late"
				} else {
					name += "/fast"
				}
				if fails {
					name += "/error"
				} else {
					name += "/false"
				}
				t.Run(name, func(t *testing.T) {
					cardAnswerWait = time.Second
					if late {
						cardAnswerWait = 10 * time.Millisecond
					}
					hold, let := heldAnswer(t)
					ix := qCard("ix_nonfinal", false, "Beijing", "Shanghai")
					var retry, answered atomic.Bool
					var deliveries atomic.Int64
					s, f, _, notes, resolves := textAnswerBot(t, func(json.RawMessage) (any, error) {
						if retry.Load() {
							deliveries.Add(1)
							answered.Store(true)
							return map[string]any{"ok": true}, nil
						}
						if late {
							hold()
						}
						if fails {
							return nil, errors.New("temporary transport failure")
						}
						return map[string]any{"ok": false, "status": status}, nil
					}, ix)
					gets := 0
					f.answerApp(AppInteractions, InteractionsGet, func(json.RawMessage) (any, error) {
						gets++
						if answered.Load() {
							return map[string]any{"ok": false, "status": "resolved"}, nil
						}
						if gets == 1 || retry.Load() {
							return map[string]any{"ok": true, "interaction": ix}, nil
						}
						return map[string]any{"ok": false, "status": status}, nil
					})
					seedCard(s, ix)
					ctx := context.Background()
					r := s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_first")
					if r == nil || r.Toast == nil || r.Card != nil {
						t.Fatalf("nonfinal response closed the card: %+v", r)
					}
					if late {
						let()
						waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(*notes) > 0 })
					} else if fails && r.Toast.Type != "error" || !fails && r.Toast.Type != "warning" {
						t.Fatalf("nonfinal response did not report a retryable press: %+v", r)
					}
					s.fbot.mu.Lock()
					_, tracked := s.fbot.cards[ix.ID]
					s.fbot.mu.Unlock()
					if !tracked || len(f.appParams(AppFeishu, FeishuUpdateCard)) != 0 || deliveries.Load() != 0 {
						t.Fatalf("nonfinal result: tracked=%v deliveries=%d", tracked, deliveries.Load())
					}
					retry.Store(true)
					accepted := s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_retry")
					if accepted == nil || accepted.Toast == nil || accepted.Toast.Type != "success" || accepted.Card == nil {
						t.Fatalf("retry was not accepted: %+v", accepted)
					}
					s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_retry")
					f.mu.Lock()
					attempts := len(*resolves)
					f.mu.Unlock()
					if attempts != 2 || deliveries.Load() != 1 {
						t.Fatalf("attempts=%d deliveries=%d", attempts, deliveries.Load())
					}
				})
			}
		}
	}
}

// An ended event can arrive after the restore lookup took its snapshot. Its
// tombstone must win over that stale pending response, without resurrecting tracking.
func TestQuestionPressEndedDuringRestore(t *testing.T) {
	defer func(w time.Duration) { cardAnswerWait = w }(cardAnswerWait)
	for _, late := range []bool{false, true} {
		for _, fails := range []bool{false, true} {
			name := "fast"
			if late {
				name = "late"
			}
			if fails {
				name += "/error"
			} else {
				name += "/false"
			}
			t.Run(name, func(t *testing.T) {
				cardAnswerWait = time.Second
				if late {
					cardAnswerWait = 10 * time.Millisecond
				}
				hold, let := heldAnswer(t)
				ix := qCard("ix_restore_race", false, "Beijing", "Shanghai")
				s, f, _, notes, resolves := textAnswerBot(t, func(json.RawMessage) (any, error) {
					if late {
						hold()
					}
					if fails {
						return nil, errors.New("temporary transport failure")
					}
					return map[string]any{"ok": false, "status": "pending"}, nil
				}, ix)
				var gets atomic.Int64
				f.answerAppBlocking(AppInteractions, InteractionsGet, func(json.RawMessage) (any, error) {
					if gets.Add(1) == 2 {
						s.InteractionEnded(context.Background(), InteractionEnded{Interaction: ix})
					}
					return map[string]any{"ok": true, "interaction": ix}, nil
				})
				seedCard(s, ix)
				r := s.botCardAction(context.Background(), ixPress(ix.ID, "Shanghai"), "oa_fake")
				if late {
					let()
					waitUntil(t, func() bool { return len(f.appParams(AppFeishu, FeishuUpdateCard)) == 1 })
					if fails {
						waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(*notes) > 0 })
					}
				} else if r == nil || r.Card == nil {
					t.Fatalf("ended card was not retired: %+v", r)
				}
				s.fbot.mu.Lock()
				_, tracked := s.fbot.cards[ix.ID]
				s.fbot.mu.Unlock()
				s.InteractionEnded(context.Background(), InteractionEnded{Interaction: ix})
				f.mu.Lock()
				attempts := len(*resolves)
				f.mu.Unlock()
				updates := len(f.appParams(AppFeishu, FeishuUpdateCard))
				want := 0
				if late {
					want = 1
				}
				if tracked || attempts != 1 || updates != want {
					t.Fatalf("tracked=%v attempts=%d updates=%d", tracked, attempts, updates)
				}
			})
		}
	}
}

func TestFastQuestionPressPreservesFinalStatus(t *testing.T) {
	for _, status := range []string{"resolved", "dismissed", "expired", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			ix := qCard("ix_final", false, "Beijing", "Shanghai")
			s, _, _, _, _ := textAnswerBot(t, func(json.RawMessage) (any, error) {
				return map[string]any{"ok": false, "status": status}, nil
			}, ix)
			seedCard(s, ix)
			ctx := context.Background()
			r := s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_fake")
			wantToast := endedText(s.botLang(ctx), status, "assistant-feishu_cards.already_handled")
			wantCard := endedText(s.botLang(ctx), status, "assistant-feishu_cards.handled")
			raw, _ := json.Marshal(r)
			if r == nil || r.Toast == nil || r.Toast.Content != wantToast || r.Card == nil || !strings.Contains(string(raw), wantCard) {
				t.Fatalf("final status %q: response=%s", status, raw)
			}
			// The fast result retires tracking without waiting for an ended notification.
			s.fbot.mu.Lock()
			_, tracked := s.fbot.cards[ix.ID]
			s.fbot.mu.Unlock()
			if tracked {
				t.Fatal("terminal result restored tracking")
			}
		})
	}
}
