package hub

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func gradingEffectiveConfig(t *testing.T, raw string) pushGradingEffective {
	t.Helper()
	cfg := parseGradingTestConfig(t, raw, false)
	return resolvePushGrading(&cfg, nil, "")
}

func gradingEffectivePolicy(level, key string) pushPolicy {
	return pushPolicy{kind: assistantwire.NotifyPush, level: level, relatedKey: key, manager: assistantwire.AssistantID}
}

func TestPushGradingEffectiveEvaluator(t *testing.T) {
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	lastAlert := map[string]time.Time{"recent": now.Add(-time.Hour)}

	t.Run("disabled grading always pushes", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":false}`)
		got := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(assistantwire.LevelP2, "recent"), lastAlert)
		if !got.Allow || got.Reason != "" || got.Level != assistantwire.LevelP2 {
			t.Fatalf("disabled outcome = %+v", got)
		}
	})

	t.Run("default actions and neutral outcomes", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true}`)
		cases := []struct {
			name   string
			level  string
			key    string
			reason string
			allow  bool
		}{
			{name: "P0 pushes", level: assistantwire.LevelP0, key: "recent", allow: true},
			{name: "P1 repeats are deduped", level: assistantwire.LevelP1, key: "recent", reason: pushGradingOutcomeDeduped},
			{name: "P2 repeats are deduped", level: assistantwire.LevelP2, key: "recent", reason: pushGradingOutcomeDeduped},
			{name: "P1 is held", level: assistantwire.LevelP1, key: "fresh", reason: pushGradingOutcomeHeld},
			{name: "P2 is silenced", level: assistantwire.LevelP2, key: "fresh", reason: pushGradingOutcomeSilenced},
			{name: "empty key cannot dedup", level: assistantwire.LevelP1, key: " ", reason: pushGradingOutcomeHeld},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(tc.level, tc.key), lastAlert)
				if got.Allow != tc.allow || got.Reason != tc.reason || got.Level != tc.level {
					t.Fatalf("outcome = %+v, want allow=%v reason=%q level=%q", got, tc.allow, tc.reason, tc.level)
				}
			})
		}
	})

	t.Run("dedup window expires", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true,"dedup_window":"1h"}`)
		got := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(assistantwire.LevelP1, "recent"), map[string]time.Time{
			"recent": now.Add(-2 * time.Hour),
		})
		if got.Allow || got.Reason != pushGradingOutcomeHeld {
			t.Fatalf("expired window outcome = %+v", got)
		}
	})

	t.Run("configured exemptions bypass dedup", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true,"dedup_exempt_levels":["P0","P1"]}`)
		got := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(assistantwire.LevelP1, "recent"), lastAlert)
		if got.Allow || got.Reason != pushGradingOutcomeHeld {
			t.Fatalf("exempt outcome = %+v", got)
		}
	})

	t.Run("configured actions choose their neutral outcomes", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"push","P2":"hold"}}`)
		pushed := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(assistantwire.LevelP1, "recent"), lastAlert)
		if !pushed.Allow || pushed.Reason != "" {
			t.Fatalf("P1 push outcome = %+v", pushed)
		}
		held := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(assistantwire.LevelP2, "fresh"), nil)
		if held.Allow || held.Reason != pushGradingOutcomeHeld || held.Level != assistantwire.LevelP2 {
			t.Fatalf("P2 hold outcome = %+v", held)
		}
		repeatHeld := evaluatePushGradingEffective(now, cfg, gradingEffectivePolicy(assistantwire.LevelP2, "recent"), lastAlert)
		if repeatHeld.Allow || repeatHeld.Reason != pushGradingOutcomeDeduped || repeatHeld.Level != assistantwire.LevelP2 {
			t.Fatalf("configured hold repeat outcome = %+v", repeatHeld)
		}

		silenced := gradingEffectiveConfig(t, `{"version":1,"enabled":true,"actions":{"P1":"silence"}}`)
		repeatSilenced := evaluatePushGradingEffective(now, silenced, gradingEffectivePolicy(assistantwire.LevelP1, "recent"), lastAlert)
		if repeatSilenced.Allow || repeatSilenced.Reason != pushGradingOutcomeDeduped || repeatSilenced.Level != assistantwire.LevelP1 {
			t.Fatalf("configured silence repeat outcome = %+v", repeatSilenced)
		}
	})

	t.Run("system alerts keep pushing", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true,"actions":{"P0":"hold"}}`)
		policy := gradingEffectivePolicy(assistantwire.LevelP2, "")
		policy.system = true
		got := evaluatePushGradingEffective(now, cfg, policy, nil)
		if !got.Allow || got.Level != assistantwire.LevelP0 || got.Reason != "" {
			t.Fatalf("system outcome = %+v", got)
		}
	})

	t.Run("finished turn without a level is P2", func(t *testing.T) {
		cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true}`)
		policy := pushPolicy{kind: assistantwire.NotifyDone, done: true, manager: assistantwire.AssistantID}
		got := evaluatePushGradingEffective(now, cfg, policy, nil)
		if got.Allow || got.Level != assistantwire.LevelP2 || got.Reason != pushGradingOutcomeSilenced {
			t.Fatalf("done outcome = %+v", got)
		}
	})
}

func TestPushGradingEffectiveMatchesLegacyDefaults(t *testing.T) {
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	lastAlert := map[string]time.Time{"repeat": now.Add(-time.Hour)}
	attention := assistantwire.Attention{Mode: assistantwire.AttentionNormal}
	cfg := gradingEffectiveConfig(t, `{"version":1,"enabled":true}`)

	cases := []struct {
		name   string
		policy pushPolicy
	}{
		{name: "P0 fresh", policy: gradingEffectivePolicy(assistantwire.LevelP0, "")},
		{name: "P1 held", policy: gradingEffectivePolicy(assistantwire.LevelP1, "")},
		{name: "P2 silenced", policy: gradingEffectivePolicy(assistantwire.LevelP2, "")},
		{name: "P0 repeats", policy: gradingEffectivePolicy(assistantwire.LevelP0, "repeat")},
		{name: "P1 dedup", policy: gradingEffectivePolicy(assistantwire.LevelP1, "repeat")},
		{name: "P2 dedup", policy: gradingEffectivePolicy(assistantwire.LevelP2, "repeat")},
		{name: "P1 dedup expires", policy: func() pushPolicy {
			policy := gradingEffectivePolicy(assistantwire.LevelP1, "expired")
			return policy
		}()},
	}
	lastAlert["expired"] = now.Add(-13 * time.Hour)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluatePushGradingEffective(now, cfg, tc.policy, lastAlert)
			legacy := evaluatePushGradingLocked(now, true, attention, nil, lastAlert, tc.policy, false)
			if got.Allow != legacy.Allow || got.Level != legacy.EffectiveLevel {
				t.Fatalf("decision mismatch: new=%+v legacy=%+v", got, legacy)
			}
			if reason := legacyPushGradingReason(got.Reason); reason != legacy.Reason {
				t.Fatalf("reason mismatch: new=%q legacy=%q decision=%+v", reason, legacy.Reason, got)
			}
		})
	}

	t.Run("disabled configuration matches grading off", func(t *testing.T) {
		disabled := gradingEffectiveConfig(t, `{"version":1,"enabled":false}`)
		for _, level := range []string{assistantwire.LevelP0, assistantwire.LevelP1, assistantwire.LevelP2} {
			policy := gradingEffectivePolicy(level, "repeat")
			got := evaluatePushGradingEffective(now, disabled, policy, lastAlert)
			legacy := evaluatePushGradingLocked(now, false, attention, nil, lastAlert, policy, false)
			if !got.Allow || !legacy.Allow || got.Level != legacy.EffectiveLevel {
				t.Fatalf("disabled mismatch for %s: new=%+v legacy=%+v", level, got, legacy)
			}
		}
	})
}

func TestPushGradingLegacyReasonAdapter(t *testing.T) {
	cases := map[string]string{
		"":                           "",
		pushGradingOutcomeHeld:       interceptReasonP1,
		pushGradingOutcomeSilenced:   interceptReasonP2,
		pushGradingOutcomeDeduped:    interceptReasonDedup,
		"unexpected_internal_reason": interceptReasonP1,
	}
	for neutral, legacy := range cases {
		if got := legacyPushGradingReason(neutral); got != legacy {
			t.Errorf("legacyPushGradingReason(%q) = %q, want %q", neutral, got, legacy)
		}
	}
}
