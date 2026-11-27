package recordingwire

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

// DebateStatus is whether a disagreement recorded in meeting minutes was settled or left open (#16728,
// docs/assistant/meeting-minutes.md "Disagreements (#16728)"). A closed type on the wire; clients show an unknown
// value with no status word.
type DebateStatus string

const (
	// DebateOpen: people put opposing claims and the meeting ended without deciding between them.
	DebateOpen DebateStatus = "open"
	// DebateSettled: people put opposing claims and the meeting later decided between them.
	DebateSettled DebateStatus = "settled"
)

// Valid reports whether s is one of the two statuses this hub writes.
func (s DebateStatus) Valid() bool { return s == DebateOpen || s == DebateSettled }

// MinutesDebatePosition is one side of a disagreement: Stance with no names in it, At the start of the transcript line
// where its holder says it (never a photo), Who only when the transcript or the calendar supports it.
type MinutesDebatePosition struct {
	Stance string  `json:"stance"`
	At     float64 `json:"at"`
	Who    string  `json:"who,omitempty"`
}

// MinutesDebateOutcome is how a settled disagreement ended: Text, At the transcript line where it was decided, and
// Decision the 0-based index in Minutes.Decisions of the kept decision citing the same line (nil when none matches).
type MinutesDebateOutcome struct {
	Text     string  `json:"text"`
	At       float64 `json:"at"`
	Decision *int    `json:"decision,omitempty"`
}

// MinutesDebate is a real disagreement in a meeting: Question, optional Topic hint, Status, At (the earliest
// position's time), 2 to 4 Positions, and Outcome when settled.
type MinutesDebate struct {
	Question  string                  `json:"question"`
	Topic     string                  `json:"topic,omitempty"`
	Status    DebateStatus            `json:"status"`
	At        float64                 `json:"at"`
	Positions []MinutesDebatePosition `json:"positions"`
	Outcome   *MinutesDebateOutcome   `json:"outcome,omitempty"`
}

// decodeMinutesDebates reads detail.minutes.debates leniently: a missing, null, non-array or unreadable block is nil,
// malformed entries are dropped, and a debate left with fewer than two valid positions is dropped, so a broken debates
// block never prevents the rest of minutes.json from decoding.
func decodeMinutesDebates(raw json.RawMessage, decisions int) []MinutesDebate {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var list []any
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	nonBlank := func(v any) (string, bool) {
		s, ok := v.(string)
		return s, ok && strings.TrimSpace(s) != ""
	}
	nonNeg := func(v any) (float64, bool) {
		f, ok := v.(float64)
		return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
	}
	var out []MinutesDebate
	for _, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		q, ok := nonBlank(obj["question"])
		if !ok {
			continue
		}
		rawPos, ok := obj["positions"].([]any)
		if !ok {
			continue
		}
		var positions []MinutesDebatePosition
		for _, pe := range rawPos {
			po, ok := pe.(map[string]any)
			if !ok {
				continue
			}
			stance, ok := nonBlank(po["stance"])
			if !ok {
				continue
			}
			pos := MinutesDebatePosition{Stance: stance}
			if at, ok := nonNeg(po["at"]); ok {
				pos.At = at
			}
			if who, ok := nonBlank(po["who"]); ok {
				pos.Who = who
			}
			positions = append(positions, pos)
		}
		if len(positions) < 2 {
			continue
		}
		d := MinutesDebate{Question: q, Positions: positions}
		if topic, ok := nonBlank(obj["topic"]); ok {
			d.Topic = topic
		}
		if st, ok := nonBlank(obj["status"]); ok {
			d.Status = DebateStatus(st)
		}
		if at, ok := nonNeg(obj["at"]); ok {
			d.At = at
		}
		if d.Status == DebateSettled {
			if oo, ok := obj["outcome"].(map[string]any); ok {
				if text, ok := nonBlank(oo["text"]); ok {
					outcome := &MinutesDebateOutcome{Text: text}
					if at, ok := nonNeg(oo["at"]); ok {
						outcome.At = at
					}
					if f, ok := nonNeg(oo["decision"]); ok && math.Trunc(f) == f {
						if idx := int(f); idx >= 0 && idx < decisions {
							outcome.Decision = &idx
						}
					}
					d.Outcome = outcome
				}
			}
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
