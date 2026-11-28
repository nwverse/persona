package recordingspeakersadapt

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/recordingspeakers"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// maxSpeakerSamples is the maximum number of longest sure-line start times returned per unmerged speaker in
// detail.speakers.people[].samples.
const maxSpeakerSamples = 3

// ImportTimeline numbers (first timeline) or pairs (later timeline) incoming engine turns against existing meeting
// state, computes per-original-id stats, updates minor marks, and evaluates meeting separation quality.
func ImportTimeline(in recordingwire.SpeakerImportInput) (recordingwire.SpeakerImportOutput, error) {
	rawTurns, err := parseWireRawTurns(in.RawTurns)
	if err != nil {
		return recordingwire.SpeakerImportOutput{}, err
	}
	covered, err := parseWireCovered(in.Covered)
	if err != nil {
		return recordingwire.SpeakerImportOutput{}, err
	}
	segs := parseWireSegments(in.Segments)
	timing := parseTimingWord(string(in.Timing.Effective()))
	attendees, f := outcome.Unpack(recordingspeakers.ParseAttendeeCount(max(0, in.Attendees)))
	if f != nil {
		return recordingwire.SpeakerImportOutput{}, wrapBadSpeakers(f)
	}
	durSec, f := outcome.Unpack(recordingspeakers.ParseSeconds(sanitizeDuration(in.Duration)))
	if f != nil {
		return recordingwire.SpeakerImportOutput{}, wrapBadSpeakers(f)
	}

	var numbered recordingspeakers.Numbered
	if in.PrevSpeakers == nil && len(in.PrevTurns) == 0 {
		numbered = recordingspeakers.Number(rawTurns)
	} else {
		prevTurns, err := parseWireTurns(in.PrevTurns)
		if err != nil {
			return recordingwire.SpeakerImportOutput{}, err
		}
		var prevRoster recordingspeakers.Roster
		if in.PrevSpeakers != nil {
			prevRoster, err = restoreWireRoster(*in.PrevSpeakers)
			if err != nil {
				return recordingwire.SpeakerImportOutput{}, err
			}
		}
		numbered = recordingspeakers.Pair(prevTurns, prevRoster, rawTurns)
	}

	var numRec numberedCollector
	numbered.Accept(&numRec)

	stats := recordingspeakers.ComputeStats(numRec.turns, segs, timing, covered)
	roster := recordingspeakers.MarkMinor(stats, numRec.roster)

	env, f := outcome.Unpack(recordingspeakers.NewAlignEnv(numRec.turns, covered, timing))
	if f != nil {
		return recordingwire.SpeakerImportOutput{}, wrapBadSpeakers(f)
	}
	labels := recordingspeakers.AlignSegments(segs, env, roster, recordingspeakers.QualityOk())
	qIn, f := outcome.Unpack(recordingspeakers.NewQualityInputBuilder(segs, labels).
		Coverage(covered, durSec).
		Context(stats, roster, attendees).
		Build())
	if f != nil {
		return recordingwire.SpeakerImportOutput{}, wrapBadSpeakers(f)
	}
	verdict := recordingspeakers.EvaluateQuality(qIn)
	quality, qualityWhy := extractQualityVerdict(verdict)

	next, people, edits := extractWireRoster(roster)
	return recordingwire.SpeakerImportOutput{
		Turns:      extractWireTurns(numRec.turns),
		Covered:    extractWireCovered(covered),
		Next:       next,
		People:     people,
		Edits:      edits,
		Stats:      extractWireStats(stats),
		Quality:    quality,
		QualityWhy: qualityWhy,
	}, nil
}

// RecomputeAfterTranscriptChange trims turns and covered spans (when TrimTo > 0), then recomputes stats, minor marks
// and quality against the current transcript of record.
func RecomputeAfterTranscriptChange(in recordingwire.SpeakerRecomputeInput) (recordingwire.SpeakerRecomputeOutput, error) {
	turns, err := parseWireTurns(in.Turns)
	if err != nil {
		return recordingwire.SpeakerRecomputeOutput{}, err
	}
	covered, err := parseWireCovered(in.Covered)
	if err != nil {
		return recordingwire.SpeakerRecomputeOutput{}, err
	}
	roster, err := restoreWireRoster(in.Speakers)
	if err != nil {
		return recordingwire.SpeakerRecomputeOutput{}, err
	}
	if in.TrimTo > 0 {
		limit, f := outcome.Unpack(recordingspeakers.ParseSeconds(sanitizeDuration(in.TrimTo)))
		if f != nil {
			return recordingwire.SpeakerRecomputeOutput{}, wrapBadSpeakers(f)
		}
		turns = recordingspeakers.TrimTurns(turns, limit)
		covered = recordingspeakers.TrimCovered(covered, limit)
	}
	segs := parseWireSegments(in.Segments)
	timing := parseTimingWord(string(in.Timing.Effective()))
	attendees, f := outcome.Unpack(recordingspeakers.ParseAttendeeCount(max(0, in.Attendees)))
	if f != nil {
		return recordingwire.SpeakerRecomputeOutput{}, wrapBadSpeakers(f)
	}
	durSec, f := outcome.Unpack(recordingspeakers.ParseSeconds(sanitizeDuration(in.Duration)))
	if f != nil {
		return recordingwire.SpeakerRecomputeOutput{}, wrapBadSpeakers(f)
	}

	stats := recordingspeakers.ComputeStats(turns, segs, timing, covered)
	roster = recordingspeakers.MarkMinor(stats, roster)

	env, f := outcome.Unpack(recordingspeakers.NewAlignEnv(turns, covered, timing))
	if f != nil {
		return recordingwire.SpeakerRecomputeOutput{}, wrapBadSpeakers(f)
	}
	labels := recordingspeakers.AlignSegments(segs, env, roster, recordingspeakers.QualityOk())
	qIn, f := outcome.Unpack(recordingspeakers.NewQualityInputBuilder(segs, labels).
		Coverage(covered, durSec).
		Context(stats, roster, attendees).
		Build())
	if f != nil {
		return recordingwire.SpeakerRecomputeOutput{}, wrapBadSpeakers(f)
	}
	verdict := recordingspeakers.EvaluateQuality(qIn)
	quality, qualityWhy := extractQualityVerdict(verdict)

	_, people, _ := extractWireRoster(roster)
	return recordingwire.SpeakerRecomputeOutput{
		Turns:      extractWireTurns(turns),
		Covered:    extractWireCovered(covered),
		People:     people,
		Stats:      extractWireStats(stats),
		Quality:    quality,
		QualityWhy: qualityWhy,
	}, nil
}

// ApplyEdit applies a batch of speaker edit operations atomically through recordingspeakers.Edit and returns the
// updated roster fields and server-computed undo operations.
func ApplyEdit(in recordingwire.SpeakerEditInput) (recordingwire.SpeakerEditOutput, error) {
	roster, err := restoreWireRoster(in.Speakers)
	if err != nil {
		return recordingwire.SpeakerEditOutput{}, err
	}
	stats, err := parseWireStats(in.Speakers.Stats)
	if err != nil {
		return recordingwire.SpeakerEditOutput{}, err
	}
	rawOps := make([]editOpWire, len(in.Ops))
	hasClear := false
	for i, op := range in.Ops {
		if op.Op == recordingwire.SpeakerOpClear {
			hasClear = true
		}
		rawOps[i] = editOpWire{
			Op:   string(op.Op),
			ID:   string(op.ID),
			Name: op.Name,
			Me:   op.Me,
			From: string(op.From),
			Into: string(op.Into),
		}
	}
	domOps, f := outcome.Unpack(parseEditOps(rawOps))
	if f != nil {
		return recordingwire.SpeakerEditOutput{}, wrapBadSpeakers(f)
	}
	edited, ef := outcome.Unpack(recordingspeakers.Edit(roster, stats, domOps))
	if ef != nil {
		return recordingwire.SpeakerEditOutput{}, wrapBadSpeakers(ef)
	}
	var rec editedCollector
	edited.Accept(&rec)
	next, people, edits := extractWireRoster(rec.roster)
	undo := extractWireEditOps(rec.undo)
	return recordingwire.SpeakerEditOutput{
		Next:    next,
		People:  people,
		Edits:   edits,
		Undo:    undo,
		Cleared: hasClear,
	}, nil
}

// ProjectDetail computes detail.speakers (with merged stats, sample start times, spans and covered) and labels
// detail.segments using recordingspeakers alignment rules.
func ProjectDetail(in recordingwire.SpeakerDetailInput) (recordingwire.SpeakerDetailOutput, error) {
	outSegs := slices.Clone(in.Segments)
	candidates := slices.Clone(in.Candidates)
	if candidates == nil {
		candidates = []string{}
	}

	ds := &kbwire.DetailSpeakers{
		Rev:        in.Rev,
		State:      in.Speakers.State,
		Reason:     in.Speakers.Reason,
		Quality:    in.Speakers.Quality,
		People:     []kbwire.DetailSpeakerPerson{},
		Candidates: candidates,
		Spans:      []kbwire.SpeakerTurn{},
		Covered:    []kbwire.SpeakerSpan{},
		Trimmed:    in.Trimmed,
	}
	if in.Turns != nil && in.Turns.Source != "" {
		ds.Source = in.Turns.Source
	} else if in.Speakers.State == recordingwire.SpeakerStateReady || in.Speakers.State == recordingwire.SpeakerStateWaiting {
		ds.Source = recordingwire.SpeakerSourceHub
	}
	if in.Speakers.State == recordingwire.SpeakerStateWaiting {
		done := in.Speakers.Done
		end := in.EndSeconds
		ds.Done = &done
		ds.End = &end
	}
	if in.Speakers.State == recordingwire.SpeakerStateCleared {
		return recordingwire.SpeakerDetailOutput{Speakers: ds, Segments: outSegs}, nil
	}

	roster, err := restoreWireRoster(in.Speakers)
	if err != nil {
		return recordingwire.SpeakerDetailOutput{}, err
	}
	rawStats, err := parseWireStats(in.Speakers.Stats)
	if err != nil {
		return recordingwire.SpeakerDetailOutput{}, err
	}
	summed := extractWireStats(recordingspeakers.SumStats(rawStats, roster))

	var samplesBySpeaker map[kbwire.SpeakerID][]float64
	if !in.Trimmed && in.Speakers.State == recordingwire.SpeakerStateReady && in.Turns != nil {
		if len(in.Turns.Turns) > 0 {
			ds.Spans = slices.Clone(in.Turns.Turns)
		}
		if len(in.Turns.Covered) > 0 {
			ds.Covered = slices.Clone(in.Turns.Covered)
		}
		domTurns, err := parseWireTurns(in.Turns.Turns)
		if err != nil {
			return recordingwire.SpeakerDetailOutput{}, err
		}
		domCovered, err := parseWireCovered(in.Turns.Covered)
		if err != nil {
			return recordingwire.SpeakerDetailOutput{}, err
		}
		timing := parseTimingWord(string(in.Timing.Effective()))
		env, f := outcome.Unpack(recordingspeakers.NewAlignEnv(domTurns, domCovered, timing))
		if f != nil {
			return recordingwire.SpeakerDetailOutput{}, wrapBadSpeakers(f)
		}
		quality := parseQualityWord(string(in.Speakers.Quality))
		var zeroQuality recordingspeakers.Quality
		if quality == zeroQuality {
			quality = recordingspeakers.QualityOk()
		}
		domSegs := parseWireSegments(outSegs)
		labels := recordingspeakers.AlignSegments(domSegs, env, roster, quality)
		samplesBySpeaker = applyLabelsAndSamples(outSegs, labels)
	}

	ds.People = make([]kbwire.DetailSpeakerPerson, 0, len(in.Speakers.People))
	for _, p := range in.Speakers.People {
		dp := kbwire.DetailSpeakerPerson{
			ID:         p.ID,
			Name:       p.Name,
			Me:         p.Me,
			MergedInto: p.MergedInto,
			Minor:      p.Minor,
		}
		if p.MergedInto == "" {
			if st, ok := summed[p.ID]; ok {
				dp.Seconds = st.Seconds
				dp.Lines = st.Lines
			}
			if samples := samplesBySpeaker[p.ID]; len(samples) > 0 {
				dp.Samples = samples
			}
		}
		ds.People = append(ds.People, dp)
	}

	return recordingwire.SpeakerDetailOutput{Speakers: ds, Segments: outSegs}, nil
}

// --- unexported conversion helpers ---

type failMessageCollector struct {
	parts []string
}

func (c *failMessageCollector) VisitInvalid(code fail.Code, part fail.Part) {
	c.parts = append(c.parts, part.Wire()+": "+code.Wire())
}

func (c *failMessageCollector) VisitRefused(code fail.Code) {
	c.parts = append(c.parts, code.Wire())
}

func (c *failMessageCollector) VisitMissing(code fail.Code) {
	c.parts = append(c.parts, code.Wire())
}

func (c *failMessageCollector) VisitConflict(code fail.Code) {
	c.parts = append(c.parts, code.Wire())
}

func wrapBadSpeakers(f fail.Fail) error {
	var c failMessageCollector
	f.Accept(&c)
	return fmt.Errorf("%w: %s", recordingwire.ErrBadSpeakers, strings.Join(c.parts, "; "))
}

func sanitizeDuration(d float64) float64 {
	if math.IsNaN(d) || math.IsInf(d, 0) || d < 0 {
		return 0
	}
	return d
}

func restoreWireRoster(s recordingwire.MeetingSpeakers) (recordingspeakers.Roster, error) {
	var zero recordingspeakers.Roster
	rec := Record{
		Next: s.Next,
		Edits: editsRecord{
			Name:    s.Edits.Name,
			Merge:   s.Edits.Merge,
			Unmerge: s.Edits.Unmerge,
			Clear:   s.Edits.Clear,
		},
	}
	if len(s.People) > 0 {
		rec.People = make([]personRecord, len(s.People))
		for i, p := range s.People {
			rec.People[i] = personRecord{
				ID:         string(p.ID),
				Name:       p.Name,
				Me:         p.Me,
				MergedInto: string(p.MergedInto),
				Minor:      p.Minor,
			}
		}
	}
	r, f := outcome.Unpack(Restore(rec))
	if f != nil {
		return zero, wrapBadSpeakers(f)
	}
	return r, nil
}

func extractWireRoster(roster recordingspeakers.Roster) (int, []recordingwire.SpeakerPerson, recordingwire.SpeakerEdits) {
	rec := Store(roster)
	people := make([]recordingwire.SpeakerPerson, 0, len(rec.People))
	for _, p := range rec.People {
		people = append(people, recordingwire.SpeakerPerson{
			ID:         recordingwire.SpeakerID(p.ID),
			Name:       p.Name,
			Me:         p.Me,
			MergedInto: recordingwire.SpeakerID(p.MergedInto),
			Minor:      p.Minor,
		})
	}
	edits := recordingwire.SpeakerEdits{
		Name:    rec.Edits.Name,
		Merge:   rec.Edits.Merge,
		Unmerge: rec.Edits.Unmerge,
		Clear:   rec.Edits.Clear,
	}
	return rec.Next, people, edits
}

func parseWireRawTurns(in []recordingwire.RawSpeakerTurn) (recordingspeakers.RawTurns, error) {
	var zero recordingspeakers.RawTurns
	list := make([]recordingspeakers.RawTurn, 0, len(in))
	for _, rt := range in {
		s, f := outcome.Unpack(recordingspeakers.ParseSeconds(rt.Start))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		e, f := outcome.Unpack(recordingspeakers.ParseSeconds(rt.End))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		sp, f := outcome.Unpack(recordingspeakers.NewSpan(s, e))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		cid, f := outcome.Unpack(recordingspeakers.ParseClusterID(string(rt.Cluster)))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		turn, f := outcome.Unpack(recordingspeakers.NewRawTurn(sp, cid))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		list = append(list, turn)
	}
	return recordingspeakers.NewRawTurns(list), nil
}

func parseWireTurns(in []recordingwire.SpeakerTurn) (recordingspeakers.Turns, error) {
	var zero recordingspeakers.Turns
	list := make([]recordingspeakers.Turn, 0, len(in))
	for _, t := range in {
		s, f := outcome.Unpack(recordingspeakers.ParseSeconds(t.Start))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		e, f := outcome.Unpack(recordingspeakers.ParseSeconds(t.End))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		sp, f := outcome.Unpack(recordingspeakers.NewSpan(s, e))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		id, f := outcome.Unpack(recordingspeakers.ParseSpeakerID(string(t.Speaker)))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		turn, f := outcome.Unpack(recordingspeakers.NewTurn(sp, id))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		list = append(list, turn)
	}
	return recordingspeakers.NewTurns(list), nil
}

func parseWireCovered(in []recordingwire.SpeakerSpan) (recordingspeakers.Covered, error) {
	var zero recordingspeakers.Covered
	spans := make([]recordingspeakers.Span, 0, len(in))
	for _, c := range in {
		s, f := outcome.Unpack(recordingspeakers.ParseSeconds(c[0]))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		e, f := outcome.Unpack(recordingspeakers.ParseSeconds(c[1]))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		sp, f := outcome.Unpack(recordingspeakers.NewSpan(s, e))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		spans = append(spans, sp)
	}
	cov, f := outcome.Unpack(recordingspeakers.NewCovered(spans))
	if f != nil {
		return zero, wrapBadSpeakers(f)
	}
	return cov, nil
}

func parseWireSegments(in []kbwire.RecordingSegment) recordingspeakers.Segments {
	list := make([]recordingspeakers.Segment, 0, len(in))
	for _, sg := range in {
		st := sanitizeDuration(sg.Start)
		en := sanitizeDuration(sg.End)
		if en < st {
			en = st
		}
		s, fs := outcome.Unpack(recordingspeakers.ParseSeconds(st))
		if fs != nil {
			continue
		}
		e, fe := outcome.Unpack(recordingspeakers.ParseSeconds(en))
		if fe != nil {
			continue
		}
		sp, fsp := outcome.Unpack(recordingspeakers.NewSpan(s, e))
		if fsp != nil {
			continue
		}
		src := recordingspeakers.LineSourceDevice()
		if strings.EqualFold(sg.Source, recordingwire.SourceHub) {
			src = recordingspeakers.LineSourceHub()
		}
		echo := recordingspeakers.EchoSpeech()
		if sg.Echo {
			echo = recordingspeakers.EchoPersona()
		}
		seg, fseg := outcome.Unpack(recordingspeakers.NewSegment(sp, src, echo, recordingspeakers.ParseLineText(sg.Text)))
		if fseg != nil {
			continue
		}
		list = append(list, seg)
	}
	return recordingspeakers.NewSegments(list)
}

func parseWireStats(in map[recordingwire.SpeakerID]recordingwire.SpeakerStat) (recordingspeakers.Stats, error) {
	var zero recordingspeakers.Stats
	if len(in) == 0 {
		return zero, nil
	}
	list := make([]recordingspeakers.SpeakerStat, 0, len(in))
	for id, st := range in {
		sid, f := outcome.Unpack(recordingspeakers.ParseSpeakerID(string(id)))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		sec, f := outcome.Unpack(recordingspeakers.ParseSeconds(st.Seconds))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		lc, f := outcome.Unpack(recordingspeakers.ParseLineCount(st.Lines))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		item, f := outcome.Unpack(recordingspeakers.NewSpeakerStat(sid, sec, lc))
		if f != nil {
			return zero, wrapBadSpeakers(f)
		}
		list = append(list, item)
	}
	stats, f := outcome.Unpack(recordingspeakers.NewStats(list))
	if f != nil {
		return zero, wrapBadSpeakers(f)
	}
	return stats, nil
}

func extractWireTurns(turns recordingspeakers.Turns) []recordingwire.SpeakerTurn {
	var rec turnsCollector
	turns.Accept(&rec)
	if rec.out == nil {
		return []recordingwire.SpeakerTurn{}
	}
	return rec.out
}

func extractWireCovered(covered recordingspeakers.Covered) []recordingwire.SpeakerSpan {
	var rec coveredCollector
	covered.Accept(&rec)
	if rec.out == nil {
		return []recordingwire.SpeakerSpan{}
	}
	return rec.out
}

func extractWireStats(stats recordingspeakers.Stats) map[recordingwire.SpeakerID]recordingwire.SpeakerStat {
	rec := statsCollector{out: map[recordingwire.SpeakerID]recordingwire.SpeakerStat{}}
	stats.Accept(&rec)
	return rec.out
}

func extractQualityVerdict(verdict recordingspeakers.QualityVerdict) (recordingwire.SpeakerQuality, recordingwire.SpeakerQualityReason) {
	var rec qualityVerdictCollector
	verdict.Accept(&rec)
	return rec.quality, rec.reason
}

func extractWireEditOps(ops recordingspeakers.EditOps) []recordingwire.SpeakerEditOp {
	var rec editOpsRecorder
	ops.Accept(&rec)
	out := make([]recordingwire.SpeakerEditOp, 0, len(rec.ops))
	for _, op := range rec.ops {
		out = append(out, recordingwire.SpeakerEditOp{
			Op:   recordingwire.SpeakerEditOpKind(op.Op),
			ID:   recordingwire.SpeakerID(op.ID),
			Name: op.Name,
			Me:   op.Me,
			From: recordingwire.SpeakerID(op.From),
			Into: recordingwire.SpeakerID(op.Into),
		})
	}
	return out
}

type sampleCandidate struct {
	start float64
	dur   float64
}

func applyLabelsAndSamples(segs []kbwire.RecordingSegment, labels recordingspeakers.LineLabels) map[kbwire.SpeakerID][]float64 {
	var col lineLabelsCollector
	labels.Accept(&col)
	bySpeaker := map[kbwire.SpeakerID][]sampleCandidate{}
	for i := range segs {
		if i >= len(col.items) {
			break
		}
		item := col.items[i]
		if !item.assigned {
			segs[i].Speaker = ""
			segs[i].SpeakerUnsure = false
			continue
		}
		segs[i].Speaker = item.speaker
		segs[i].SpeakerUnsure = item.unsure
		if !item.unsure {
			dur := segs[i].End - segs[i].Start
			if dur < 0 {
				dur = 0
			}
			bySpeaker[item.speaker] = append(bySpeaker[item.speaker], sampleCandidate{start: segs[i].Start, dur: dur})
		}
	}
	samples := make(map[kbwire.SpeakerID][]float64, len(bySpeaker))
	for id, cands := range bySpeaker {
		slices.SortFunc(cands, func(a, b sampleCandidate) int {
			if c := cmp.Compare(b.dur, a.dur); c != 0 {
				return c
			}
			return cmp.Compare(a.start, b.start)
		})
		if len(cands) > maxSpeakerSamples {
			cands = cands[:maxSpeakerSamples]
		}
		starts := make([]float64, len(cands))
		for i, c := range cands {
			starts[i] = c.start
		}
		slices.Sort(starts)
		samples[id] = starts
	}
	return samples
}

// --- visitors ---

type numberedCollector struct {
	turns  recordingspeakers.Turns
	roster recordingspeakers.Roster
}

func (n *numberedCollector) VisitNumbered(turns recordingspeakers.Turns, roster recordingspeakers.Roster) {
	n.turns, n.roster = turns, roster
}

type editedCollector struct {
	roster recordingspeakers.Roster
	undo   recordingspeakers.EditOps
}

func (e *editedCollector) VisitEdited(roster recordingspeakers.Roster, undo recordingspeakers.EditOps) {
	e.roster, e.undo = roster, undo
}

type spanCollector struct{ start, end float64 }

func (s *spanCollector) VisitSpan(start, end recordingspeakers.Seconds) {
	s.start, s.end = start.Wire(), end.Wire()
}

type turnCollector struct {
	span spanCollector
	out  recordingwire.SpeakerTurn
}

func (t *turnCollector) VisitTurn(span recordingspeakers.Span, speaker recordingspeakers.SpeakerID) {
	span.Accept(&t.span)
	t.out = recordingwire.SpeakerTurn{
		Start:   t.span.start,
		End:     t.span.end,
		Speaker: recordingwire.SpeakerID(speaker.Wire()),
	}
}

type turnsCollector struct {
	turn turnCollector
	out  []recordingwire.SpeakerTurn
}

func (t *turnsCollector) VisitTurn(turn recordingspeakers.Turn) {
	turn.Accept(&t.turn)
	t.out = append(t.out, t.turn.out)
}

type coveredCollector struct {
	span spanCollector
	out  []recordingwire.SpeakerSpan
}

func (c *coveredCollector) VisitSpan(span recordingspeakers.Span) {
	span.Accept(&c.span)
	c.out = append(c.out, recordingwire.SpeakerSpan{c.span.start, c.span.end})
}

type statCollector struct {
	id   recordingwire.SpeakerID
	stat recordingwire.SpeakerStat
}

func (s *statCollector) VisitSpeakerStat(speaker recordingspeakers.SpeakerID, seconds recordingspeakers.Seconds, lines recordingspeakers.LineCount) {
	s.id = recordingwire.SpeakerID(speaker.Wire())
	s.stat = recordingwire.SpeakerStat{Seconds: seconds.Wire(), Lines: lines.Wire()}
}

type statsCollector struct {
	item statCollector
	out  map[recordingwire.SpeakerID]recordingwire.SpeakerStat
}

func (s *statsCollector) VisitSpeakerStat(stat recordingspeakers.SpeakerStat) {
	stat.Accept(&s.item)
	s.out[s.item.id] = s.item.stat
}

type qualityVerdictCollector struct {
	qw      qualityWord
	rw      qualityReasonWord
	quality recordingwire.SpeakerQuality
	reason  recordingwire.SpeakerQualityReason
}

func (q *qualityVerdictCollector) VisitQualityVerdict(quality recordingspeakers.Quality, reason opt.Option[recordingspeakers.QualityReason]) {
	q.qw.word = ""
	quality.Accept(&q.qw)
	q.quality = recordingwire.SpeakerQuality(q.qw.word)
	q.reason = ""
	reason.Match(func(r recordingspeakers.QualityReason) {
		q.rw.word = ""
		r.Accept(&q.rw)
		q.reason = recordingwire.SpeakerQualityReason(q.rw.word)
	}, func() {})
}

type lineLabelItem struct {
	assigned bool
	speaker  kbwire.SpeakerID
	unsure   bool
}

type lineLabelCollector struct {
	cert certaintyBool
	item lineLabelItem
}

func (l *lineLabelCollector) VisitAssigned(speaker recordingspeakers.SpeakerID, certainty recordingspeakers.Certainty) {
	certainty.Accept(&l.cert)
	l.item = lineLabelItem{
		assigned: true,
		speaker:  kbwire.SpeakerID(speaker.Wire()),
		unsure:   l.cert.unsure,
	}
}

func (l *lineLabelCollector) VisitSkipped(recordingspeakers.SkipReason) {
	l.item = lineLabelItem{}
}

type lineLabelsCollector struct {
	one   lineLabelCollector
	items []lineLabelItem
}

func (l *lineLabelsCollector) VisitLineLabel(label recordingspeakers.LineLabel) {
	l.one.item = lineLabelItem{}
	label.Accept(&l.one)
	l.items = append(l.items, l.one.item)
}
