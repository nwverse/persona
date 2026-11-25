package hub

import (
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// Neutral outcomes used by the versioned push grading evaluator. The current
// live boundary keeps its persisted intercept reasons; the adapter below is the
// only place that translates between the two vocabularies.
const (
	pushGradingOutcomeHeld     = "held"
	pushGradingOutcomeSilenced = "silenced"
	pushGradingOutcomeDeduped  = "deduped"
)

type pushGradingOutcome struct {
	Allow  bool
	Level  string
	Reason string
}

// evaluatePushGradingEffective applies the versioned effective configuration
// without mutating notification, dedup, intercept, or delivery state. Attention
// composition and main-conversation level derivation remain at their existing
// live and preview boundaries in this slice.
func evaluatePushGradingEffective(
	now time.Time,
	cfg pushGradingEffective,
	policy pushPolicy,
	lastAlert map[string]time.Time,
) pushGradingOutcome {
	level := normalizePushLevel(levelFor(policy.kind, policy.level))
	if policy.system {
		level = assistantwire.LevelP0
	} else if policy.done && policy.level == "" {
		level = assistantwire.LevelP2
	}
	outcome := pushGradingOutcome{Allow: true, Level: level}
	if policy.system || !cfg.Enabled {
		return outcome
	}

	action := cfg.Actions.P1
	switch level {
	case assistantwire.LevelP0:
		action = cfg.Actions.P0
	case assistantwire.LevelP2:
		action = cfg.Actions.P2
	}
	if action == "push" {
		return outcome
	}

	// Versioned level actions are policy configuration rather than the legacy
	// ordered-rule engine. A direct push still bypasses repeat suppression;
	// configured hold and silence preserve the versioned model's dedup-first
	// outcome, which the live adapter maps to the legacy dedup reason.
	key := strings.TrimSpace(policy.relatedKey)
	if key != "" && !slices.Contains(cfg.DedupExemptLevels, level) {
		if last, ok := lastAlert[key]; ok && now.Sub(last) < cfg.DedupWindow {
			outcome.Allow = false
			outcome.Reason = pushGradingOutcomeDeduped
			return outcome
		}
	}

	outcome.Allow = false
	switch action {
	case "silence":
		outcome.Reason = pushGradingOutcomeSilenced
	default:
		// Configuration validation restricts actions to push, hold, and silence.
		// Keep an unexpected internal value fail-closed rather than delivering it.
		outcome.Reason = pushGradingOutcomeHeld
	}
	return outcome
}

// legacyPushGradingReason translates neutral evaluator outcomes at the current
// live boundary. Unknown internal values fail closed as a held P1 push; the
// versioned evaluator itself only emits the three neutral held outcomes above.
func legacyPushGradingReason(reason string) string {
	switch reason {
	case "":
		return ""
	case pushGradingOutcomeSilenced:
		return interceptReasonP2
	case pushGradingOutcomeDeduped:
		return interceptReasonDedup
	default:
		return interceptReasonP1
	}
}
