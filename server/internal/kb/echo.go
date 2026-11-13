package kb

import (
	"encoding/json"
	"math"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Persona's echo in a meeting it speaks in (#5905, docs/assistant/meeting-minutes.md "Persona's echo"): the meeting's
// microphone hears Persona's answers too, and the transcript cannot tell voices apart. A line is Persona's echo only
// when it overlaps a stretch Persona spoke in (widened by echoMargin) and its words are what Persona said there
// (echoContainment of its character bigrams). Echo lines are kept and marked (RecordingSegment.Echo), and left out of
// what is built from the meeting: the minutes, the summary, the follow-up. The phone's copy of the rule is
// apple/Shared/Recording/MeetingEcho.swift; both run the cases in spec/fixtures/meeting-echo.

// echoMargin widens a span by this many seconds on both sides: the recognizer's times are a little off. A starting
// value, to calibrate against labelled recordings.
const echoMargin = 0.5

// echoContainment is the share of a line's distinct character bigrams that must be among those of what Persona said.
// A starting value, to calibrate against labelled recordings.
const echoContainment = 0.6

// Limits of an upload's spoken: more spans than maxSpokenSpans ignores the list, a text over maxSpokenText bytes is cut
// at a character boundary.
const (
	maxSpokenSpans = 1000
	maxSpokenText  = 4000
)

// SpokenSpan is a stretch Persona spoke in, seconds on the recording's clock, with what it said (the upload's spoken).
// Text is empty for a span the phone saved before #5905: such a span keeps the old time rule.
type SpokenSpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text,omitempty"`
}

// readSpoken is the upload's spoken, leniently: a value that is not a list, or a list over maxSpokenSpans, is no spans;
// a span that is not an object with numeric, finite start and end (end not before start) and a string text, if any, is
// left out. It never fails the upload: the same request carries the transcript.
func readSpoken(raw json.RawMessage) []SpokenSpan {
	if jsonNull(raw) {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) > maxSpokenSpans {
		return nil
	}
	var out []SpokenSpan
	for _, item := range items {
		var sp struct {
			Start *float64 `json:"start"`
			End   *float64 `json:"end"`
			Text  *string  `json:"text"`
		}
		if json.Unmarshal(item, &sp) != nil || sp.Start == nil || sp.End == nil || !finite(*sp.Start) || !finite(*sp.End) || *sp.End < *sp.Start {
			continue
		}
		span := SpokenSpan{Start: *sp.Start, End: *sp.End}
		if sp.Text != nil {
			span.Text = cutBytes(*sp.Text, maxSpokenText)
		}
		out = append(out, span)
	}
	return out
}

// MeetingLines is segs without Persona's echo: what the summary, the minutes and the follow-up read.
func MeetingLines(segs []RecordingSegment) []RecordingSegment {
	out := make([]RecordingSegment, 0, len(segs))
	for _, sg := range segs {
		if !sg.Echo {
			out = append(out, sg)
		}
	}
	return out
}

// markEcho marks the segments that are Persona's echo by spans; a segment the phone marked stays marked.
func markEcho(segs []RecordingSegment, spans []SpokenSpan) {
	if len(spans) == 0 {
		return
	}
	for i := range segs {
		if !segs[i].Echo {
			segs[i].Echo = isEcho(segs[i].Start, segs[i].End, segs[i].Text, spans)
		}
	}
}

// isEcho: the line start–end saying text is Persona's echo by one of spans.
func isEcho(start, end float64, text string, spans []SpokenSpan) bool {
	for _, sp := range spans {
		if sp.echoes(start, end, text) {
			return true
		}
	}
	return false
}

// echoes: the line is Persona's echo by this span. A span without text keeps the time rule from before #5905 (holds).
func (sp SpokenSpan) echoes(start, end float64, text string) bool {
	if sp.Text == "" {
		return sp.holds(start, end)
	}
	return sp.overlaps(start, end) && echoMatches(text, sp.Text)
}

// overlaps: the line overlaps the span widened by echoMargin on both sides; a line of no length, when it starts inside.
func (sp SpokenSpan) overlaps(start, end float64) bool {
	from, to := sp.Start-echoMargin, sp.End+echoMargin
	if end <= start {
		return start >= from && start <= to
	}
	return end >= from && start <= to
}

// holds is the time rule from before #5905: at least half of the line inside the span (no margin); a line of no
// length, when it starts inside.
func (sp SpokenSpan) holds(start, end float64) bool {
	length := end - start
	if length <= 0 {
		return start >= sp.Start && start <= sp.End
	}
	inside := math.Min(end, sp.End) - math.Max(start, sp.Start)
	return inside/length >= 0.5
}

// echoMatches: the words of line are what spoken said. Both are normalized (echoNormal); then at least echoContainment of
// the line's distinct character bigrams are among the spoken text's. A line of one character is echo only when the
// spoken text holds it; a line with nothing left is never echo.
func echoMatches(line, spoken string) bool {
	l := echoNormal(line)
	if len(l) == 0 {
		return false
	}
	s := echoNormal(spoken)
	if len(l) < 2 {
		for _, r := range s {
			if r == l[0] {
				return true
			}
		}
		return false
	}
	said := map[[2]rune]bool{}
	for i := 1; i < len(s); i++ {
		said[[2]rune{s[i-1], s[i]}] = true
	}
	mine := map[[2]rune]bool{}
	found := 0
	for i := 1; i < len(l); i++ {
		b := [2]rune{l[i-1], l[i]}
		if mine[b] {
			continue
		}
		mine[b] = true
		if said[b] {
			found++
		}
	}
	return float64(found)/float64(len(mine)) >= echoContainment
}

// echoNormal is text as the echo rule compares it: Unicode NFKC, lower case, letters (L*) and decimal digits (Nd) only;
// punctuation, spaces, symbols and marks go.
func echoNormal(text string) []rune {
	var out []rune
	for _, r := range norm.NFKC.String(text) {
		r = unicode.ToLower(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return out
}
