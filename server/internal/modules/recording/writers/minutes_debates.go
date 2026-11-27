package writers

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Disagreements in meeting minutes (#16728, docs/assistant/meeting-minutes.md "Disagreements (#16728)"):
// answerDebate decodes the model's debates leniently (reusing modelText, modelLine and modelList); buildDebates checks
// citations (transcript lines only, never photos), enforces caps, links settled outcomes to kept decisions and drops
// invalid entries without ever making the answer unusable; debateNotesForMerge carries part disagreements into the
// merging call with their own quota; renderDebates writes the Markdown section for both layouts.

const (
	minutesDebatesMax       = 6   // disagreements kept per meeting
	minutesDebatePosMin     = 2   // fewer valid positions drops the disagreement
	minutesDebatePosMax     = 4   // positions kept per disagreement
	minutesDebateQRunes     = 200 // question at most, as conclusions
	minutesMergeDebateTotal = 24  // total debate notes budget across parts (separate from minutesMergeItems)
)

// answerDebatePosition is one side of a disagreement in the model's answer.
type answerDebatePosition struct {
	Stance modelText `json:"stance"`
	Line   modelLine `json:"line"`
	Who    modelText `json:"who"`
}

// answerDebateOutcome is how a settled disagreement ended in the model's answer.
type answerDebateOutcome struct {
	Text modelText `json:"text"`
	Line modelLine `json:"line"`
}

// modelDebateOutcome reads a debate's outcome leniently: a non-object value (string, number, array, null) becomes nil
// rather than failing the enclosing debate or answer.
type modelDebateOutcome struct {
	val *answerDebateOutcome
}

func (o *modelDebateOutcome) UnmarshalJSON(data []byte) error {
	o.val = nil
	b := bytes.TrimSpace(data)
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	var x answerDebateOutcome
	if json.Unmarshal(b, &x) == nil {
		o.val = &x
	}
	return nil
}

// answerDebate is a disagreement of the model's answer; malformed entries are dropped by modelList.
type answerDebate struct {
	Question  modelText                       `json:"question"`
	Topic     modelText                       `json:"topic"`
	Status    modelText                       `json:"status"`
	Positions modelList[answerDebatePosition] `json:"positions"`
	Outcome   modelDebateOutcome              `json:"outcome"`
}

// buildDebates checks the model's disagreements against lineAt (transcript lines read, never photos), links settled
// outcomes to kept decisions citing the same transcript line, checks who with named, sets each debate's At to its
// earliest position's At, sorts by At, and caps at minutesDebatesMax (and minutesDebatePosMax positions each).
// Disagreements never count towards the answer's usability citation check.
func buildDebates(raw []answerDebate, lineAt map[int]float64, decisions []placed[MinutesItem], named func(string) string) []MinutesDebate {
	if len(raw) == 0 {
		return nil
	}
	decisionByLine := map[int]int{}
	for i, d := range decisions {
		if d.line > 0 {
			if _, exists := decisionByLine[d.line]; !exists {
				decisionByLine[d.line] = i
			}
		}
	}
	seenQ := map[string]bool{}
	var out []MinutesDebate
	for _, d := range raw {
		q := cardLine(string(d.Question), minutesDebateQRunes)
		if q == "" {
			continue
		}
		qKey := strings.ToLower(q)
		if seenQ[qKey] {
			continue
		}
		seenStance := map[string]bool{}
		var positions []MinutesDebatePosition
		for _, pos := range d.Positions {
			if len(positions) == minutesDebatePosMax {
				break
			}
			stance := cardLine(string(pos.Stance), minutesTextRunes)
			if stance == "" || seenStance[stance] {
				continue
			}
			sec, ok := lineAt[int(pos.Line)]
			if !ok {
				continue
			}
			seenStance[stance] = true
			positions = append(positions, MinutesDebatePosition{
				Stance: stance,
				At:     sec,
				Who:    named(string(pos.Who)),
			})
		}
		if len(positions) < minutesDebatePosMin {
			continue
		}
		slices.SortStableFunc(positions, func(x, y MinutesDebatePosition) int { return cmp.Compare(x.At, y.At) })
		status := DebateOpen
		if strings.EqualFold(strings.TrimSpace(stripMarks(string(d.Status))), string(DebateSettled)) {
			status = DebateSettled
		}
		var outcome *MinutesDebateOutcome
		if status == DebateSettled {
			if o := d.Outcome.val; o != nil {
				text := cardLine(string(o.Text), minutesTextRunes)
				outLine := int(o.Line)
				if sec, ok := lineAt[outLine]; ok && text != "" {
					outcome = &MinutesDebateOutcome{Text: text, At: sec}
					if idx, ok := decisionByLine[outLine]; ok {
						i := idx
						outcome.Decision = &i
					}
				}
			}
			if outcome == nil {
				status = DebateOpen
			}
		}
		seenQ[qKey] = true
		out = append(out, MinutesDebate{
			Question:  q,
			Topic:     cardLine(string(d.Topic), minutesTopicRunes),
			Status:    status,
			At:        positions[0].At,
			Positions: positions,
			Outcome:   outcome,
		})
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortStableFunc(out, func(x, y MinutesDebate) int { return cmp.Compare(x.At, y.At) })
	return firstN(out, minutesDebatesMax)
}

// debateNotesForMerge extracts a chunk's disagreements for the merging call, keeping only transcript lines present in
// lines (never photos), ordered by earliest position and capped to limit (separate from the other sections' per-part
// quota). A single-position candidate from one part is kept in the notes so the merging call can pair it with the
// opposing side from another part; final verification in buildDebates still requires at least two positions.
func debateNotesForMerge(a minutesAnswer, lines map[int]int, limit int) []prompts.KBMinutesDebate {
	if len(a.Debates) == 0 || limit <= 0 {
		return nil
	}
	type placedDebate struct {
		debate prompts.KBMinutesDebate
		at     int
	}
	var list []placedDebate
	for _, d := range a.Debates {
		q := clipRunes(stripMarks(string(d.Question)), minutesNoteRunes)
		if q == "" {
			continue
		}
		var positions []prompts.KBMinutesDebatePosition
		earliest := -1
		for _, pos := range d.Positions {
			if len(positions) == minutesDebatePosMax {
				break
			}
			stance := clipRunes(stripMarks(string(pos.Stance)), minutesNoteRunes)
			if stance == "" {
				continue
			}
			at, ok := lines[int(pos.Line)]
			if !ok {
				continue
			}
			if earliest < 0 || at < earliest {
				earliest = at
			}
			positions = append(positions, prompts.KBMinutesDebatePosition{
				Line:   int(pos.Line),
				Stance: stance,
				Who:    plainLine(string(pos.Who)),
			})
		}
		if len(positions) == 0 {
			continue
		}
		note := prompts.KBMinutesDebate{
			Question:  q,
			Topic:     cardLine(string(d.Topic), minutesTopicRunes),
			Positions: positions,
		}
		if strings.EqualFold(strings.TrimSpace(stripMarks(string(d.Status))), string(DebateSettled)) && d.Outcome.val != nil {
			outText := clipRunes(stripMarks(string(d.Outcome.val.Text)), minutesNoteRunes)
			if _, ok := lines[int(d.Outcome.val.Line)]; ok && outText != "" {
				note.Settled = true
				note.OutcomeText = outText
				note.OutcomeLine = int(d.Outcome.val.Line)
			}
		}
		list = append(list, placedDebate{debate: note, at: earliest})
	}
	if len(list) == 0 {
		return nil
	}
	slices.SortStableFunc(list, func(x, y placedDebate) int { return cmp.Compare(x.at, y.at) })
	var out []prompts.KBMinutesDebate
	for _, x := range firstN(list, limit) {
		out = append(out, x.debate)
	}
	return out
}

// debates writes the Disagreements section of minutes.md, or nothing when list is empty (keeping minutes without
// disagreements byte for byte unchanged).
func (w *minutesMarkdown) debates(list []MinutesDebate) {
	if len(list) == 0 {
		return
	}
	b, l := &w.b, w.l
	b.WriteString("\n## " + i18n.T(l, "kb-minutes.debates") + "\n")
	for _, d := range list {
		b.WriteString("\n### " + w.esc(d.Question))
		switch d.Status {
		case DebateOpen:
			b.WriteString(i18n.T(l, "kb-minutes.debate_status", i18n.Args{"status": i18n.T(l, "kb-minutes.debate_open")}))
		case DebateSettled:
			b.WriteString(i18n.T(l, "kb-minutes.debate_status", i18n.Args{"status": i18n.T(l, "kb-minutes.debate_settled")}))
		}
		b.WriteByte('\n')
		for _, p := range d.Positions {
			b.WriteString("- " + w.at(p.At) + w.esc(p.Stance))
			if p.Who != "" {
				b.WriteString(i18n.T(l, "kb-minutes.who", i18n.Args{"who": w.esc(p.Who)}))
			}
			b.WriteByte('\n')
		}
		if d.Status == DebateSettled && d.Outcome != nil && d.Outcome.Text != "" {
			b.WriteString("- " + w.at(d.Outcome.At) + i18n.T(l, "kb-minutes.debate_outcome") + w.esc(d.Outcome.Text) + "\n")
		}
	}
}

// remapDebateDecisions updates each settled debate's Outcome.Decision index after photo-citing decisions leave
// m.Decisions (dropPhotos): remap[old] is the new index, or -1 if that decision was removed.
func remapDebateDecisions(m *Minutes, remap []int) {
	for i := range m.Debates {
		o := m.Debates[i].Outcome
		if o == nil || o.Decision == nil {
			continue
		}
		old := *o.Decision
		if old < 0 || old >= len(remap) || remap[old] < 0 {
			o.Decision = nil
		} else {
			n := remap[old]
			o.Decision = &n
		}
	}
}
