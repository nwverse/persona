package kb

// The echo rule (docs/assistant/meeting-minutes.md phase 4 "Echo"), in one place: a sentence at least half inside a
// span when Persona spoke, each span widened by half a second on each side, is Persona's own voice heard back and is
// left out (of the live log, the room and the hub's transcript). Everything that judges a sentence against the
// speaking spans calls EchoOf and EchoHeld, so a refinement of the rule (a content check of what Persona said) changes
// these two functions and no caller. TestEchoRuleIsOneFunction keeps it so.

// EchoSentence is a sentence the echo rule judges, in recording seconds.
type EchoSentence struct {
	Start, End float64
	Text       string
}

// EchoSpan is a span Persona spoke in, in recording seconds; Text is what it said, when known (unused today).
type EchoSpan struct {
	From, To float64
	Text     string
}

// EchoVerdict is what the echo rule makes of a sentence.
type EchoVerdict string

// The echo rule's verdicts.
const (
	EchoKeep EchoVerdict = "keep"
	EchoDrop EchoVerdict = "drop"
)

// EchoMargin widens every speaking span on each side, in seconds.
const EchoMargin = 0.5

// EchoSpans are the timeline's speaking spans as the echo rule reads them.
func EchoSpans(t Timeline) []EchoSpan {
	if len(t.Speaking) == 0 {
		return nil
	}
	out := make([]EchoSpan, 0, len(t.Speaking))
	for _, s := range t.Speaking {
		out = append(out, EchoSpan{From: s[0], To: s[1]})
	}
	return out
}

// EchoOf is the echo rule: EchoDrop when at least half of [Start, End] is inside the spans widened by EchoMargin on
// each side (taken together), else EchoKeep. A sentence of no length is dropped when its moment is inside one.
func EchoOf(s EchoSentence, spans []EchoSpan) EchoVerdict {
	if len(spans) == 0 {
		return EchoKeep
	}
	wide := make([][2]float64, 0, len(spans))
	for _, sp := range spans {
		wide = append(wide, [2]float64{sp.From - EchoMargin, sp.To + EchoMargin})
	}
	if CoveredShare(s.Start, s.End, wide) >= 0.5 {
		return EchoDrop
	}
	return EchoKeep
}

// EchoHeld: a sentence that ends after an open speaking span began (less EchoMargin) waits for the span to close
// before EchoOf judges it ("No echo goes out first"); openFrom is when that span began.
func EchoHeld(s EchoSentence, openFrom float64) bool {
	return s.End > openFrom-EchoMargin
}
