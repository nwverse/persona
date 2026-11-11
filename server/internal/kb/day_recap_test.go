package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
)

// The daily recap (day_recap.go, day_recap_write.go): placeholders only.

// dayFake is a model for the recap: a chunk's notes (kb/day_recap) and the merge (kb/day_recap_merge) made from what
// it is given, unless note or merge answer instead.
type dayFake struct {
	mu          sync.Mutex
	notes       []string // the chunks' prompts, in order
	merges      []string // the merges' prompts
	system      []string
	note, merge func(user string) (string, error)
}

var (
	fakeLine   = regexp.MustCompile(`(?m)^\[(\d+)\] (\d\d:\d\d) (.*)$`)
	fakeEntry  = regexp.MustCompile(`^- \[(\d+)–(\d+)\] (（像在开会）)?(.*)$`)
	fakePoint  = regexp.MustCompile(`^  - \[(\d+)\] (.*?)(（([^（）]*)）)?$`)
	fakeAction = regexp.MustCompile(`^- \[(\d+)\] (.*)$`)
)

func (m *dayFake) JSONMax(_ context.Context, system, user string, _ int, out any) error {
	m.mu.Lock()
	merge := strings.Contains(system, "写这一天的全天回顾")
	if merge {
		m.merges = append(m.merges, user)
	} else {
		m.notes = append(m.notes, user)
	}
	m.system = append(m.system, system)
	note, mergeF := m.note, m.merge
	m.mu.Unlock()
	var reply string
	var err error
	switch {
	case merge && mergeF != nil:
		reply, err = mergeF(user)
	case merge:
		reply = fakeMerge(user)
	case note != nil:
		reply, err = note(user)
	default:
		reply = fakeNotes(user)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(reply), out)
}

func (m *dayFake) counts() (notes, merges int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notes), len(m.merges)
}

// fakeNotes notes a chunk as one entry of all its lines (a meeting when one says 会议), a point per line, and an
// action of each line saying 负责.
func fakeNotes(user string) string {
	type item struct {
		Text string `json:"text,omitempty"`
		What string `json:"what,omitempty"`
		Line int    `json:"line"`
		Who  string `json:"who"`
	}
	var points, actions []item
	kind := RecapOutside
	first, last := 0, 0
	for _, l := range fakeLine.FindAllStringSubmatch(user, -1) {
		n, _ := strconv.Atoi(l[1])
		if first == 0 {
			first = n
		}
		last = n
		points = append(points, item{Text: l[3], Line: n})
		if strings.Contains(l[3], "会议") {
			kind = RecapMeeting
		}
		if strings.Contains(l[3], "负责") {
			actions = append(actions, item{What: l[3], Line: n, Who: "Bo"})
		}
	}
	raw, _ := json.Marshal(map[string]any{"summary": "这一段说了几件事", "entries": []map[string]any{{"from_line": first, "to_line": last,
		"kind": kind, "title": "一段", "points": points}}, "actions": actions})
	return string(raw)
}

// fakeMerge makes the recap of the notes it is given, entry for entry.
func fakeMerge(user string) string {
	type point struct {
		Text string `json:"text"`
		Line int    `json:"line"`
		Who  string `json:"who"`
	}
	type entry struct {
		From   int     `json:"from_line"`
		To     int     `json:"to_line"`
		Kind   string  `json:"kind"`
		Title  string  `json:"title"`
		Points []point `json:"points"`
	}
	var timeline []entry
	var actions []map[string]any
	inActions := false
	for _, l := range strings.Split(user, "\n") {
		switch {
		case strings.HasPrefix(l, "待办："):
			inActions = true
		case strings.HasPrefix(l, "## "):
			inActions = false
		case inActions && fakeAction.MatchString(l):
			m := fakeAction.FindStringSubmatch(l)
			n, _ := strconv.Atoi(m[1])
			who, what, _ := strings.Cut(m[2], "：")
			actions = append(actions, map[string]any{"who": who, "what": what, "line": n})
		case fakeEntry.MatchString(l):
			m := fakeEntry.FindStringSubmatch(l)
			from, _ := strconv.Atoi(m[1])
			to, _ := strconv.Atoi(m[2])
			kind := RecapOutside
			if m[3] != "" {
				kind = RecapMeeting
			}
			timeline = append(timeline, entry{From: from, To: to, Kind: kind, Title: m[4]})
		case fakePoint.MatchString(l) && len(timeline) > 0:
			m := fakePoint.FindStringSubmatch(l)
			n, _ := strconv.Atoi(m[1])
			timeline[len(timeline)-1].Points = append(timeline[len(timeline)-1].Points, point{Text: m[2], Line: n, Who: m[4]})
		}
	}
	raw, _ := json.Marshal(map[string]any{"summary": "这一天主要在忙导出。", "timeline": timeline, "actions": actions})
	return string(raw)
}

// writeTestRecap has recording id's recap written now (asked for), with dayFake.
func writeTestRecap(t *testing.T, e *Engine, id string) {
	t.Helper()
	old := e.Model
	e.Model = &dayFake{}
	defer func() { e.Model = old }()
	if err := e.AskMinutes(id); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, id); r.RecapState != MinutesReady {
		t.Fatalf("recap state %q", r.RecapState)
	}
}

const dayNotesReply = `{
  "summary": "上午在讨论导出的发布",
  "entries": [
    {"from_line": 1, "to_line": 3, "kind": "meeting", "title": "导出评审", "points": [
      {"text": "导出超过 4 MB 的条目会被丢掉", "line": 2, "who": "Ada"},
      {"text": "编造的要点", "line": 99}
    ]},
    {"from_line": 7, "to_line": 8, "kind": "outside", "title": "没有引用的", "points": [{"text": "无处可引", "line": 40}]}
  ],
  "actions": [{"who": "Bo", "what": "补上回归测试", "by": "周五前", "due_date": "2026-11-13", "line": "[3]"}]
}`

// A finished hour is noted once as text arrives (kb/day_recap, golden), and a recap asked for merges the notes: stored
// beside the recording, readable from Get with its summary, searched in recap.md only, and never pushed.
func TestDayRecapWritten(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	m := &captureModel{reply: dayNotesReply}
	e := dayEngine(t, m, &now)
	var told []DayRecap
	e.DayRecapWritten = func(_ context.Context, _ Recording, d DayRecap) { told = append(told, d) }
	u := dayUpload(1, "大家好，今天评审导出", "Ada 说导出超过 4 MB 的条目会被丢掉", "Bo 你周五前补上回归测试")
	u.Coverage.To = 7200 // to 10:00: the 8 o'clock hour is finished
	mustPut(t, e, dayID, u, now)
	if n := e.WriteDayRecaps(context.Background(), 1); n != 1 {
		t.Fatalf("worked on %d", n)
	}
	m.golden(t, "day_recap")
	notes := e.Store.readRecapNotes(dayID)
	if len(notes.Chunks) != 1 || !notes.Chunks[0].Noted || len(notes.Chunks[0].Entries) != 1 ||
		len(notes.Chunks[0].Entries[0].Points) != 1 || len(notes.Chunks[0].Actions) != 1 {
		t.Fatalf("notes %+v", notes)
	}
	// Noted once: nothing to do until it changes.
	if n := e.WriteDayRecaps(context.Background(), 1); n != 0 || len(m.calls) != 0 {
		t.Fatalf("noted again: %d %d", n, len(m.calls))
	}
	if r := recordingNow(t, e, dayID); r.RecapState != "" {
		t.Fatalf("state %q before any recap is due", r.RecapState)
	}

	f := &dayFake{merge: func(string) (string, error) {
		return `{"summary": "评审了导出的上限。", "timeline": [
		  {"from_line": 1, "to_line": 3, "kind": "outside", "title": "导出评审", "meeting": 0, "points": [
		    {"text": "导出超过 4 MB 的条目会被丢掉", "line": 2, "who": "Ada"}, {"text": "没引用", "line": 50}]},
		  {"from_line": 1, "to_line": 1, "kind": "outside", "title": "空的", "points": [{"text": "不在笔记里", "line": 1}]}],
		  "actions": [{"who": "Bo", "what": "补上回归测试", "by": "周五前", "due_date": "2026-11-13", "line": 3},
		    {"who": "Zed", "what": "只是一段的开头", "line": 1}]}`, nil
	}}
	e.Model = f
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	r := recordingNow(t, e, dayID)
	if r.RecapState != MinutesPending || r.RecapAsks != 1 || r.MinutesAsked || r.MinutesState != "" || r.RecapDue.Reason != RecapDueAsked {
		t.Fatalf("asked: %+v", r)
	}
	if n := e.WriteDayRecaps(context.Background(), 1); n != 1 {
		t.Fatalf("recap: worked on %d", n)
	}
	if notes, merges := f.counts(); notes != 0 || merges != 1 {
		t.Fatalf("calls: %d notes, %d merges (the hour was noted already)", notes, merges)
	}
	if !strings.Contains(f.merges[0], "- [1–3] （像在开会）导出评审\n  - [2] 导出超过 4 MB 的条目会被丢掉（Ada）") ||
		!strings.Contains(f.merges[0], "- [3] Bo：补上回归测试（期限：周五前）（2026-11-13）") {
		t.Errorf("merge input:\n%s", f.merges[0])
	}
	rc, err := e.ReadDayRecap(dayID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Version != 1 || rc.Revision != 1 || rc.Day != "2026-11-11" || rc.Final || rc.Partial || rc.Summary != "评审了导出的上限。" ||
		rc.Covers != (RecapSpan{0, 3600}) {
		t.Errorf("recap head %+v", rc)
	}
	// Line 1 only starts an entry of the notes, which cite nothing there: the second entry has no point left, and
	// the action citing it goes too.
	if len(rc.Timeline) != 1 || rc.Timeline[0].From != 0 || rc.Timeline[0].To != 120 || len(rc.Timeline[0].Points) != 1 ||
		rc.Timeline[0].Points[0] != (MinutesPoint{Text: "导出超过 4 MB 的条目会被丢掉", At: 60, Who: "Ada"}) {
		t.Errorf("timeline %+v", rc.Timeline)
	}
	if len(rc.Actions) != 1 || rc.Actions[0] != (MinutesAction{ID: "a1", Who: "Bo", What: "补上回归测试", By: "周五前", DueDate: "2026-11-13", At: 120}) {
		t.Errorf("actions %+v", rc.Actions)
	}
	for _, want := range []string{"# 2026-11-11 全天回顾", "评审了导出的上限。", "- 时段：08:00–09:00（截至目前）", "## 会议以外",
		"- 08:00–08:02 导出评审", "  - [08:01] 导出超过 4 MB 的条目会被丢掉（Ada）", "## 建议待办", "- [08:02] Bo：补上回归测试（周五前）"} {
		if !strings.Contains(rc.Markdown, want) {
			t.Errorf("markdown lacks %q:\n%s", want, rc.Markdown)
		}
	}
	if md, _ := os.ReadFile(e.Store.Path(KindRecording, dayID, recapMarkdown)); strings.TrimSpace(string(md)) != rc.Markdown {
		t.Errorf("recap.md %q", md)
	}
	r = recordingNow(t, e, dayID)
	if r.RecapState != MinutesReady || r.RecapVersion != 1 || r.RecapRev != 1 || r.RecapDue != nil || r.RecapFinal || r.RecapFinalAt != nil {
		t.Errorf("after: %+v", r)
	}
	if len(told) != 0 {
		t.Errorf("a recap asked for before the recap time pushed: %+v", told)
	}
	d, err := e.Get(KindRecording, dayID, 0)
	if err != nil || !strings.Contains(string(d.Recap), `"补上回归测试"`) || d.Summary != "评审了导出的上限。\n" {
		t.Fatalf("Get: %v %s %q", err, d.Recap, d.Summary)
	}
	// Searched in recap.md, never in recap.json or the notes.
	var files []string
	for _, h := range e.Search("回归测试", false, 40) {
		files = append(files, h.Path)
	}
	all := strings.Join(files, " ")
	if !strings.Contains(all, "recordings/"+dayID+"/recap.md") || strings.Contains(all, "recap.json") || strings.Contains(all, recapNotesFile) {
		t.Errorf("search hits %v", files)
	}
	if hits := e.SearchKind("回归测试", KindRecording, false, 40); len(hits) == 0 {
		t.Error("kind=recordings finds nothing in recap.md")
	}
	found := e.Find("上限", 10)
	if len(found) != 1 || found[0].Entry.ID != dayID {
		t.Errorf("find %+v", found)
	}
	if ms := e.Matches(KindRecording, dayID, "回归测试", 40); !slices.ContainsFunc(ms, func(m Match) bool { return strings.HasSuffix(m.Path, "/recap.md") }) {
		t.Errorf("matches %+v", ms)
	}
	if hits := e.Search("编造的要点", false, 40); len(hits) != 0 {
		t.Errorf("the notes are searched: %v", hits)
	}
}

// Notes are made once per finished chunk: the hour in progress waits, a late sentence notes its chunk again and
// nothing else, and a PUT keeps the notes of the chunks it did not change.
func TestDayRecapNotesIncremental(t *testing.T) {
	now := dayClock.Add(90 * time.Minute)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	u := dayUpload(1)
	u.Segments = []RecordingSegment{seg(0, "八点的事"), seg(600, "八点十分"), seg(3700, "九点的事")}
	u.Coverage = &RecordingCoverage{To: 5400} // 09:30
	mustPut(t, e, dayID, u, now)
	e.WriteDayRecaps(context.Background(), 1)
	if n, _ := f.counts(); n != 1 || !strings.Contains(f.notes[0], "八点十分") || strings.Contains(f.notes[0], "九点的事") {
		t.Fatalf("notes %d: %v", n, f.notes)
	}
	if e.WriteDayRecaps(context.Background(), 1); len(f.notes) != 1 {
		t.Fatalf("noted again: %d", len(f.notes))
	}
	// The 9 o'clock hour finishes.
	a := dayAppend(1, 2, 3, seg(4000, "九点多"))
	a.Coverage = &RecordingCoverage{To: 7300}
	if got := mustAppend(t, e, dayID, a); !got.Wake {
		t.Error("an hour finished without a wake")
	}
	e.WriteDayRecaps(context.Background(), 1)
	if n, _ := f.counts(); n != 2 || !strings.Contains(f.notes[1], "[4] 09:06 九点多") {
		t.Fatalf("the 9 o'clock hour: %d %v", n, f.notes)
	}
	// A late sentence of 8 o'clock: that hour again, only.
	if got := mustAppend(t, e, dayID, dayAppend(1, 3, 4, seg(1200, "迟到的一句"))); !got.Wake {
		t.Error("a late sentence without a wake")
	}
	e.WriteDayRecaps(context.Background(), 1)
	if n, _ := f.counts(); n != 3 || !strings.Contains(f.notes[2], "[5] 08:20 迟到的一句") || strings.Contains(f.notes[2], "九点") {
		t.Fatalf("late: %d %v", n, f.notes)
	}
	if notes := e.Store.readRecapNotes(dayID); len(notes.Chunks) != 2 {
		t.Errorf("stale notes kept: %+v", notes.Chunks)
	}
	// A repair PUT with the same lines: no chunk is read again. One that erased the 9 o'clock line: that hour only.
	segs := e.Store.RecordingSegments(dayID)
	slices.SortFunc(segs, func(a, b RecordingSegment) int { return a.N - b.N })
	put := dayUpload(10)
	for _, s := range segs {
		s.N = 0
		put.Segments = append(put.Segments, s)
	}
	put.Coverage = &RecordingCoverage{To: 7300}
	mustPut(t, e, dayID, put, now)
	e.WriteDayRecaps(context.Background(), 1)
	if n, _ := f.counts(); n != 3 {
		t.Fatalf("a PUT that changed nothing was noted again: %d", n)
	}
	erased := dayUpload(11)
	for _, s := range segs {
		if s.N = 0; s.Text == "九点的事" {
			s.Text = "" // erased, its position kept
		}
		erased.Segments = append(erased.Segments, s)
	}
	erased.Coverage = &RecordingCoverage{To: 7300, Gaps: []CoverageGap{{From: 3690, To: 3720, Reason: GapErased}}}
	mustPut(t, e, dayID, erased, now)
	e.WriteDayRecaps(context.Background(), 1)
	if n, _ := f.counts(); n != 4 || strings.Contains(f.notes[3], "九点的事") || !strings.Contains(f.notes[3], "九点多") {
		t.Fatalf("after the erase: %d %v", n, f.notes)
	}
}

// A chunk's notes are tried like minutes, counted per chunk on its base: three unusable answers give it up (the recap
// leaves it out, partial), a try that ran out of time waits before the next; text arriving in another hour resets
// neither.
func TestDayRecapNotesRetries(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	f := &dayFake{note: func(user string) (string, error) {
		switch {
		case strings.Contains(user, "八点"):
			return `{"entries": "not a list"}`, nil
		case strings.Contains(user, "九点"):
			return "", fmt.Errorf("call: %w", context.DeadlineExceeded)
		}
		return fakeNotes(user), nil
	}}
	e := dayEngine(t, f, &now)
	u := dayUpload(1)
	u.Segments = []RecordingSegment{seg(0, "八点"), seg(3700, "九点")}
	u.Coverage = &RecordingCoverage{To: 7200}
	mustPut(t, e, dayID, u, now)
	chunk := func(text string) recapChunkNote {
		for _, c := range e.Store.readRecapNotes(dayID).Chunks {
			if c.FromN == map[string]int{"八点": 1, "九点": 2}[text] {
				return c
			}
		}
		return recapChunkNote{}
	}
	for i := range minutesTries + 1 {
		a := dayAppend(1, int64(2+i), 2+i, seg(float64(7200+60*i), "十点以后"))
		a.Coverage = &RecordingCoverage{To: float64(7200 + 60*(i+1))}
		mustAppend(t, e, dayID, a)
		e.WriteDayRecaps(context.Background(), 1)
	}
	if c := chunk("八点"); c.Noted || c.Fails != minutesTries || c.Base != 1 {
		t.Errorf("unusable: %+v", c)
	}
	nine := chunk("九点")
	if nine.Noted || nine.Timeouts != 1 || !nine.RetryAt.After(now) {
		t.Errorf("ran out of time: %+v", nine)
	}
	if notes, _ := f.counts(); notes != minutesTries+1 {
		t.Errorf("%d notes calls: given up, or tried again before its time", notes)
	}
	// After the wait the 9 o'clock hour is noted; asked for, the recap leaves the 8 o'clock hour out.
	now = nine.RetryAt.Add(time.Second)
	f.note = nil
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 1)
	rc, err := e.ReadDayRecap(dayID)
	if err != nil || !rc.Partial || strings.Contains(f.merges[0], "[1] 八点") || !strings.Contains(f.merges[0], "[2] 九点") {
		t.Fatalf("recap %+v %v\n%s", rc, err, strings.Join(f.merges, "\n"))
	}
}

// When a recap is due: at the recap time (30 minutes before the evening routine's, in the owner's zone) with text not
// read yet, once; when complete (final); 6 hours after its day without (final, cutoff); asked; never without text.
// The day's one push goes with the first recap written at or after the recap time, across its parts.
func TestDayRecapDue(t *testing.T) {
	now := dayClock.Add(12 * time.Hour) // 20:00
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	var told []string
	e.DayRecapWritten = func(_ context.Context, r Recording, d DayRecap) {
		told = append(told, r.ID+" v"+strconv.Itoa(d.Version))
	}
	const part2, empty = "2a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d", "5a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"
	u := dayUpload(1, "早上开会", "Bo 负责发布")
	u.Coverage.To = 11 * 3600
	mustPut(t, e, dayID, u, now)
	quiet := dayUpload(1)
	quiet.Coverage.To = 11 * 3600
	mustPut(t, e, empty, quiet, now)
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, dayID); r.RecapState != "" || r.RecapDue != nil {
		t.Fatalf("due before the recap time: %+v", r.RecapDue)
	}
	// 21:00: the evening routine at 21:30 by default.
	now = dayClock.Add(13 * time.Hour)
	e.WriteDayRecaps(context.Background(), 5)
	r := recordingNow(t, e, dayID)
	if r.RecapState != MinutesReady || r.RecapEvening != "2026-11-11" || r.RecapVersion != 1 {
		t.Fatalf("at the recap time: %+v", r)
	}
	if len(told) != 1 || told[0] != dayID+" v1" {
		t.Fatalf("told %v", told)
	}
	if q := recordingNow(t, e, empty); q.RecapState != "" || q.RecapVersion != 0 {
		t.Errorf("no text, yet a recap: %+v", q)
	}
	// Once a day: new text after it is not recapped again at the recap time, nor pushed again.
	a := dayAppend(1, 2, 2, seg(13*3600, "晚上的事"))
	a.Coverage = &RecordingCoverage{To: 13*3600 + 60}
	mustAppend(t, e, dayID, a)
	now = now.Add(10 * time.Minute)
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, dayID); r.RecapVersion != 1 || r.RecapDue != nil {
		t.Errorf("evening again: %+v", r)
	}
	// Another part of the day written later: no second push.
	p2 := dayUpload(5, "第二部分")
	p2.Part, p2.Coverage.To = 2, 13*3600
	mustPut(t, e, part2, p2, now)
	if err := e.AskMinutes(part2); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, part2); r.RecapState != MinutesReady || len(told) != 1 {
		t.Errorf("part 2: %q, told %v", r.RecapState, told)
	}
	// Complete: the final recap, no push.
	c := dayAppend(1, 3, 3)
	c.Complete = ptr(true)
	mustAppend(t, e, dayID, c)
	e.WriteDayRecaps(context.Background(), 5)
	rc, _ := e.ReadDayRecap(dayID)
	r = recordingNow(t, e, dayID)
	if !rc.Final || rc.FinalReason != FinalComplete || !r.RecapFinal || r.RecapFinalAt == nil || r.RecapState != MinutesReady || len(told) != 1 {
		t.Errorf("complete: %+v %+v told %v", rc, r, told)
	}
	kept, _ := e.RecordingStates()
	for _, s := range kept {
		if s.ID == dayID && (!s.RecapFinal || s.RecapFinalAt == nil || s.RecapState != MinutesReady) {
			t.Errorf("reconciliation %+v", s)
		}
	}

}

// 6 hours after its day ended, a recording not complete gets its final recap of what arrived, pushed when its day had
// none; a later complete writes another final one, not pushed.
func TestDayRecapCutoff(t *testing.T) {
	now := time.Date(2026, 11, 12, 5, 59, 0, 0, goldenZone)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	var told []string
	e.DayRecapWritten = func(_ context.Context, r Recording, d DayRecap) {
		told = append(told, r.ID+" v"+strconv.Itoa(d.Version))
	}
	mustPut(t, e, dayID, dayUpload(7, "昨天的事"), now)
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, dayID); r.RecapDue != nil || r.RecapVersion != 0 {
		t.Fatalf("before its cutoff: %+v", r.RecapDue)
	}
	now = time.Date(2026, 11, 12, 6, 0, 0, 0, goldenZone)
	e.WriteDayRecaps(context.Background(), 5)
	rc, _ := e.ReadDayRecap(dayID)
	if rc == nil || !rc.Final || rc.FinalReason != FinalCutoff || recordingNow(t, e, dayID).RecapFinalAt == nil {
		t.Fatalf("cutoff: %+v", rc)
	}
	if len(told) != 1 || told[0] != dayID+" v1" {
		t.Errorf("told %v", told)
	}
	c := dayAppend(7, 8, 1)
	c.Complete = ptr(true)
	mustAppend(t, e, dayID, c)
	e.WriteDayRecaps(context.Background(), 5)
	if rc, _ = e.ReadDayRecap(dayID); rc.Version != 2 || rc.FinalReason != FinalComplete || len(told) != 1 {
		t.Errorf("complete after the cutoff: %+v told %v", rc, told)
	}
}

// The recap time follows the evening routine's (DayRecapClock), in the owner's zone; a recording ready then pushes
// at once, and a pending push is told again after a restart.
func TestDayRecapClockAndRetell(t *testing.T) {
	now := dayClock.Add(11 * time.Hour) // 19:00
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	e.DayRecapClock = func() (int, int) { return 19, 45 }
	var told []string
	e.DayRecapWritten = func(_ context.Context, r Recording, _ DayRecap) { told = append(told, r.ID) }
	mustPut(t, e, dayID, dayUpload(1, "早上开会"), now)
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 5)
	if len(told) != 0 {
		t.Fatalf("an asked recap before the recap time pushed: %v", told)
	}
	// 19:15: the recap time; nothing new to read, so the ready recap goes with the push.
	now = dayClock.Add(11*time.Hour + 15*time.Minute)
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, dayID); r.RecapVersion != 1 || len(told) != 1 || !r.RecapPushed || r.RecapPush {
		t.Fatalf("at the recap time: v%d told %v %+v", r.RecapVersion, told, r)
	}
	// A push marked but never told (a restart in between): told at the next round, then done.
	r := recordingNow(t, e, dayID)
	r.RecapPush, r.RecapPushed = true, false
	_ = e.Store.putEntity(KindRecording, dayID, r, &r.Tagging, false)
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, dayID); len(told) != 2 || r.RecapPush || !r.RecapPushed {
		t.Errorf("retold %v %+v", told, r)
	}
	e.WriteDayRecaps(context.Background(), 5)
	if len(told) != 2 {
		t.Errorf("told again %v", told)
	}
}

// The day's push is marked due in the same write as the recap that takes it, before DayRecapWritten is told, and
// stays due until a hook returned (a restart in between, or no hook yet). A day whose recap time came with no recap
// ready waits: its first recap after it pushes, even one asked for.
func TestDayRecapPushPersisted(t *testing.T) {
	now := dayClock.Add(13 * time.Hour) // 21:00, the recap time
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	mustPut(t, e, dayID, dayUpload(1, "早上开会"), now)
	e.WriteDayRecaps(context.Background(), 1)
	r := recordingNow(t, e, dayID)
	if r.RecapVersion != 1 || !r.RecapPush || r.RecapWait || r.RecapPushed {
		t.Fatalf("no hook yet: %+v", r)
	}
	var told []int
	e.DayRecapWritten = func(_ context.Context, got Recording, d DayRecap) {
		if cur := recordingNow(t, e, got.ID); !cur.RecapPush || cur.RecapVersion != d.Version || !got.RecapPush {
			t.Errorf("told before the push was stored: %+v", cur)
		}
		told = append(told, d.Version)
	}
	e.WriteDayRecaps(context.Background(), 1)
	e.WriteDayRecaps(context.Background(), 1)
	if r := recordingNow(t, e, dayID); len(told) != 1 || r.RecapPush || !r.RecapPushed {
		t.Errorf("told %v %+v", told, r)
	}

	// Another day: the recap time comes while the model fails, so the day waits; the user asks later that evening,
	// and that recap pushes.
	next := dayClock.AddDate(0, 0, 1)
	now = next.Add(13 * time.Hour)
	u := dayUpload(1, "第二天的事")
	u.Day, u.StartedAt = "2026-11-12", next
	mustPut(t, e, recIDDay2, u, now)
	f.merge = func(string) (string, error) { return `{"summary": ""}`, nil }
	for range minutesTries {
		e.WriteDayRecaps(context.Background(), 1)
	}
	r = recordingNow(t, e, recIDDay2)
	if r.RecapState != MinutesFailed || !r.RecapWait || r.RecapPush || len(told) != 1 {
		t.Fatalf("failed at the recap time: %q %+v told %v", r.RecapState, r, told)
	}
	f.merge = nil
	now = now.Add(time.Hour)
	if err := e.AskMinutes(recIDDay2); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 1)
	if r := recordingNow(t, e, recIDDay2); r.RecapState != MinutesReady || r.RecapWait || !r.RecapPushed || len(told) != 2 {
		t.Errorf("the first recap after the recap time: %+v told %v", r, told)
	}
}

// At the recap time a recording still open counts as recorded until now (within its day), however far its coverage
// reaches: one in another zone whose day began before the owner's is recapped that evening.
func TestDayRecapOpenSpan(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 11, 0, 0, 0, 0, tokyo) // 2026-11-10 23:00 in the owner's zone
	now := time.Date(2026, 11, 11, 21, 0, 0, 0, goldenZone)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	u := dayUpload(1, "刚过零点")
	u.StartedAt, u.TZ = start, "Asia/Tokyo"
	u.Coverage.To = 1800 // the phone is far behind
	mustPut(t, e, dayID, u, now)
	e.WriteDayRecaps(context.Background(), 1)
	if r := recordingNow(t, e, dayID); r.RecapEvening != "2026-11-11" || r.RecapVersion != 1 {
		t.Errorf("an open recording was not recapped at the recap time: %q v%d", r.RecapEvening, r.RecapVersion)
	}
	// Ended before the owner's day began: not tonight's.
	u = dayUpload(1, "前一天的事")
	end := start.Add(30 * time.Minute)
	u.StartedAt, u.TZ, u.EndedAt, u.Day = start.AddDate(0, 0, -1), "Asia/Tokyo", &end, "2026-11-10"
	mustPut(t, e, recIDDay2, u, now)
	e.WriteDayRecaps(context.Background(), 1)
	if r := recordingNow(t, e, recIDDay2); r.RecapEvening != "" || r.RecapDue != nil {
		t.Errorf("an ended recording of another day: %q %+v", r.RecapEvening, r.RecapDue)
	}
	// The evening routine reads the same day recordings, in their own zone.
	today := e.TodaysDayRecordings(now)
	if len(today) != 1 || today[0].ID != dayID || e.Store.RecordingZone(&today[0]).String() != "Asia/Tokyo" {
		t.Errorf("today's %+v", today)
	}
	if got := e.TodaysDayRecordings(now.AddDate(0, 0, 1)); len(got) != 0 {
		t.Errorf("tomorrow's %+v", got)
	}
}

// The model's tries: an unusable merge three times makes the recap failed, asking again makes it pending; a merge that
// ran out of time waits before its next try; new text resets neither.
func TestDayRecapFailures(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	f := &dayFake{merge: func(string) (string, error) {
		return `{"summary": "", "timeline": [{"title": "x", "points": [{"text": "y", "line": 77}]}]}`, nil
	}}
	e := dayEngine(t, f, &now)
	mustPut(t, e, dayID, dayUpload(1, "早上开会"), now)
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		e.WriteDayRecaps(context.Background(), 1)
		mustAppend(t, e, dayID, dayAppend(1, int64(2+i), 1+i, seg(float64(120+60*i), "又一句")))
	}
	r := recordingNow(t, e, dayID)
	if r.RecapState != MinutesFailed || r.RecapDue.Fails != 3 {
		t.Fatalf("after 3 unusable answers: %q %+v", r.RecapState, r.RecapDue)
	}
	e.WriteDayRecaps(context.Background(), 1)
	if _, merges := f.counts(); merges != 3 {
		t.Errorf("a failed recap tried again: %d", merges)
	}
	f.merge = func(string) (string, error) { return "", fmt.Errorf("call: %w", context.DeadlineExceeded) }
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 1)
	r = recordingNow(t, e, dayID)
	if r.RecapState != MinutesPending || r.RecapDue.Timeouts != 1 || !r.RecapDue.RetryAt.After(now) {
		t.Fatalf("timed out: %q %+v", r.RecapState, r.RecapDue)
	}
	e.WriteDayRecaps(context.Background(), 1)
	if _, merges := f.counts(); merges != 4 {
		t.Errorf("tried again before its time: %d", merges)
	}
	now = r.RecapDue.RetryAt.Add(time.Second)
	f.merge = nil
	e.WriteDayRecaps(context.Background(), 1)
	if r := recordingNow(t, e, dayID); r.RecapState != MinutesReady {
		t.Errorf("after the wait: %q", r.RecapState)
	}
	// A paused model leaves it due, counting nothing.
	f.merge = func(string) (string, error) {
		return "", &ai.PausedError{Pause: ai.Pause{Reason: ai.ReasonQuota, Until: now.Add(time.Hour)}}
	}
	_ = e.AskMinutes(dayID)
	e.WriteDayRecaps(context.Background(), 1)
	if r := recordingNow(t, e, dayID); r.RecapState != MinutesPending || r.RecapDue.Fails != 0 || r.RecapDue.Timeouts != 0 {
		t.Errorf("paused: %+v", r.RecapDue)
	}
	// No text: asking is refused.
	mustPut(t, e, recIDDay2, dayUpload(1), now)
	if err := e.AskMinutes(recIDDay2); !errors.Is(err, ErrRecordingIncomplete) {
		t.Errorf("ask without text: %v", err)
	}
}

const recIDDay2 = "7a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"

// The merge's checks: known meetings as hints (a meeting recording overlapping the day, calendar items starting that
// day), an entry inside a meeting recording is that meeting, one the model matched by number is that one; gaps inside
// covers; who only when said.
func TestDayRecapMergeChecks(t *testing.T) {
	now := dayClock.Add(5 * time.Hour)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	const meetingID = "8a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"
	mu := meetingUpload(1, dayClock.Add(time.Hour), "评审开始", "评审结束")
	mu.Title = "设计评审"
	mu.Duration = 1800
	putRecording(t, e, meetingID, mu, now)
	for _, it := range []*Item{
		{Kind: KindEvent, ID: "e1", Title: "午饭约", At: dayClock.Add(4 * time.Hour)},
		{Kind: KindEvent, ID: "e2", Title: "昨天的会", At: dayClock.Add(-20 * time.Hour)},
	} {
		_ = e.Store.PutItem(it)
	}
	u := dayUpload(1)
	u.Segments = []RecordingSegment{seg(3700, "评审里说了导出"), seg(3800, "Ada 说先保留"), seg(14500, "午饭时说的")}
	u.Coverage = &RecordingCoverage{To: 5 * 3600, Gaps: []CoverageGap{{From: 600, To: 900, Reason: GapInterrupted},
		{From: 20000, To: 21000, Reason: GapPaused}}}
	mustPut(t, e, dayID, u, now)
	f.merge = func(user string) (string, error) {
		return `{"summary": "开了评审，中午吃饭。", "timeline": [
		  {"from_line": 1, "to_line": 2, "kind": "outside", "title": "", "meeting": 0, "points": [{"text": "说了导出", "line": 1, "who": "Ada"}, {"text": "先保留", "line": 2, "who": "Zed"}]},
		  {"from_line": 3, "to_line": 3, "kind": "meeting", "title": "午饭", "meeting": 2, "points": [{"text": "午饭时说的", "line": 3}]}
		], "actions": []}`, nil
	}
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 1)
	_, merges := f.counts()
	if merges != 1 || !strings.Contains(f.merges[0], "1. 会议录音「设计评审」09:00–09:30") || !strings.Contains(f.merges[0], "2. 日程「午饭约」12:00") ||
		strings.Contains(f.merges[0], "昨天的会") {
		t.Fatalf("hints:\n%s", strings.Join(f.merges, "\n---\n"))
	}
	rc, err := e.ReadDayRecap(dayID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Timeline) != 2 {
		t.Fatalf("timeline %+v", rc.Timeline)
	}
	first, second := rc.Timeline[0], rc.Timeline[1]
	if first.Kind != RecapMeeting || first.Meeting == nil || *first.Meeting != (MeetingRef{Kind: KindRecording, ID: meetingID, Name: "设计评审"}) ||
		first.Title != "设计评审" || first.From != 3700 || first.To != 3800 {
		t.Errorf("inside the meeting recording: %+v", first)
	}
	if first.Points[0].Who != "Ada" || first.Points[1].Who != "" {
		t.Errorf("who: %+v", first.Points)
	}
	if second.Kind != RecapMeeting || second.Meeting == nil || second.Meeting.ID != "e1" || second.Meeting.Kind != KindEvent {
		t.Errorf("matched by number: %+v", second)
	}
	if rc.Covers.To != 5*3600 || !slices.Equal(rc.Gaps, []CoverageGap{{600, 900, GapInterrupted}}) {
		t.Errorf("covers %+v gaps %+v", rc.Covers, rc.Gaps)
	}
	if !strings.Contains(rc.Markdown, "## 会议\n\n- 09:01–09:03 设计评审") || !strings.Contains(rc.Markdown, "- 没有录到：08:10–08:15（被打断）") {
		t.Errorf("markdown:\n%s", rc.Markdown)
	}
	// A merge with only a summary is a recap of a day with nothing to note.
	f.merge = func(string) (string, error) {
		return `{"summary": "没什么特别的。", "timeline": [], "actions": []}`, nil
	}
	_ = e.AskMinutes(dayID)
	e.WriteDayRecaps(context.Background(), 1)
	if rc, _ := e.ReadDayRecap(dayID); rc.Version != 2 || len(rc.Timeline) != 0 || rc.Summary != "没什么特别的。" {
		t.Errorf("summary only: %+v", rc)
	}
}

// A day too long for one merge reads its first and newest chunks only, and says it is partial.
func TestDayRecapPartial(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	u := dayUpload(1)
	line := strings.Repeat("长", minutesLineRunes)
	for i := range 2 * (recapMaxChunks + 5) { // two lines a chunk, all in the 8 o'clock hour
		u.Segments = append(u.Segments, RecordingSegment{Start: float64(i * 10), End: float64(i*10 + 5), Text: strconv.Itoa(i) + line})
	}
	u.Coverage = &RecordingCoverage{To: 3600}
	mustPut(t, e, dayID, u, now)
	if err := e.AskMinutes(dayID); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 1)
	notes, merges := f.counts()
	if notes != recapMaxChunks || merges != 1 {
		t.Fatalf("%d notes, %d merges", notes, merges)
	}
	rc, err := e.ReadDayRecap(dayID)
	if err != nil {
		t.Fatal(err)
	}
	if !rc.Partial || !strings.Contains(rc.Markdown, "中间有些时段没有整理进回顾") || !strings.Contains(f.merges[0], "这一天太长") {
		t.Errorf("partial %v", rc.Partial)
	}
	// The first chunks and the newest are read; the middle is not.
	if !strings.Contains(f.merges[0], "[1] 0") || !strings.Contains(f.merges[0], "[40] 39") || strings.Contains(f.merges[0], "[41] 40") ||
		strings.Contains(f.merges[0], "[50] 49") || !strings.Contains(f.merges[0], "[51] 50") || !strings.Contains(f.merges[0], "[130] 129") {
		t.Errorf("chunks read: %d bytes", len(f.merges[0]))
	}
}

// Confirming a recap's to-dos: by version, stale without by-task, by task otherwise; a later version keeps a confirmed
// to-do on the same who and what said within 15 minutes, one action each.
func TestDayRecapTodos(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	mustPut(t, e, dayID, dayUpload(1, "Bo 负责发布", "Ada 负责测试", "早上开会"), now)
	writeTestRecap(t, e, dayID)
	e.Model = f
	rc, _ := e.ReadDayRecap(dayID)
	if len(rc.Actions) != 2 || rc.Actions[0].What != "Bo 负责发布" || rc.Actions[0].Who != "Bo" || rc.Actions[1].What != "Ada 负责测试" {
		t.Fatalf("actions %+v", rc.Actions)
	}
	if _, err := e.SetDayRecapTodos(dayID, 9, map[string]string{"a1": "todo_x"}, nil); !errors.Is(err, ErrMinutesStale) {
		t.Errorf("stale: %v", err)
	}
	got, err := e.SetDayRecapTodos(dayID, rc.Version, map[string]string{"a1": "todo_1"}, nil)
	if err != nil || got.Actions[0].TodoID != "todo_1" || got.Actions[1].TodoID != "" {
		t.Fatalf("set: %+v %v", got, err)
	}
	if again, _ := e.ReadDayRecap(dayID); again.Actions[0].TodoID != "todo_1" || again.Version != rc.Version {
		t.Errorf("stored %+v", again)
	}
	// Written again: the confirmed to-do stays on the same task said within 15 minutes, on one action only.
	_ = e.AskMinutes(dayID)
	e.WriteDayRecaps(context.Background(), 1)
	rc2, _ := e.ReadDayRecap(dayID)
	if rc2.Version != rc.Version+1 || rc2.Actions[0].TodoID != "todo_1" || rc2.Actions[1].TodoID != "" {
		t.Errorf("kept %+v", rc2.Actions)
	}
	// By task, for a version written since.
	got, err = e.SetDayRecapTodos(dayID, rc.Version, map[string]string{"a2": "todo_2"}, map[string]string{MinutesTaskKey(rc.Actions[1]): "todo_2"})
	if err != nil || got.Actions[1].TodoID != "todo_2" {
		t.Errorf("by task %+v %v", got, err)
	}
	// Said again much later: another task.
	todos := []RecapTodo{{Task: recapTaskKey(" bo ", "BO 负责发布"), At: 0, Todo: "todo_1"}}
	later := &DayRecap{Actions: []MinutesAction{{Who: "Bo", What: "Bo 负责发布", At: 3600}, {Who: "Bo", What: "Bo 负责发布", At: 300}}}
	keepRecapTodos(later, todos)
	if later.Actions[0].TodoID != "" || later.Actions[1].TodoID != "todo_1" {
		t.Errorf("near %+v", later.Actions)
	}
}

// A PUT setting a new base while a recap exists withdraws at once what it no longer says: entries overlapping an
// erased gap, points and actions whose line is gone (a confirmed one too: its to-do stays, and comes back on the same
// action in a later version), entries left empty, and the summary; another recap is due, without a push.
func TestDayRecapRepair(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	var told []string
	e.DayRecapWritten = func(_ context.Context, r Recording, _ DayRecap) { told = append(told, r.ID) }
	u := dayUpload(1, "早上开会", "Bo 负责发布", "Ada 负责测试", "zebrapassword 说了不该说的")
	u.Coverage.To = 3600
	mustPut(t, e, dayID, u, now)
	f.merge = func(string) (string, error) {
		return `{"summary": "提到了 zebrapassword。", "timeline": [
		  {"from_line": 1, "to_line": 3, "kind": "meeting", "title": "早会", "points": [{"text": "早上开会", "line": 1}, {"text": "Ada 负责测试", "line": 3}]},
		  {"from_line": 4, "to_line": 4, "kind": "outside", "title": "zebrapassword", "points": [{"text": "说了不该说的", "line": 4}]}
		], "actions": [{"who": "Bo", "what": "负责发布", "line": 2}, {"who": "Ada", "what": "负责测试", "line": 3}]}`, nil
	}
	writeTestRecapWith(t, e, dayID, f)
	rc, _ := e.ReadDayRecap(dayID)
	if _, err := e.SetDayRecapTodos(dayID, rc.Version, map[string]string{"a1": "todo_bo"}, nil); err != nil {
		t.Fatal(err)
	}
	if hits := e.Search("zebrapassword", false, 10); len(hits) == 0 {
		t.Fatal("not found before the erase")
	}
	// The phone erased 08:03 (line 4, which zebrapassword was said on) and dropped line 2 (Bo's) and 3 (Ada's),
	// positions kept.
	repair := dayUpload(5, "早上开会", "", "", "")
	repair.Coverage = &RecordingCoverage{To: 3600, Gaps: []CoverageGap{{From: 170, To: 200, Reason: GapErased}}}
	mustPut(t, e, dayID, repair, now)
	rc, _ = e.ReadDayRecap(dayID)
	if rc.Summary != "" || len(rc.Timeline) != 1 || len(rc.Timeline[0].Points) != 1 || rc.Timeline[0].Points[0].Text != "早上开会" {
		t.Errorf("withdrawn %+v", rc)
	}
	if len(rc.Actions) != 0 {
		t.Errorf("actions whose line is gone kept: %+v", rc.Actions)
	}
	if rc.Version != 2 || !slices.Equal(rc.Gaps, []CoverageGap{{170, 200, GapErased}}) {
		t.Errorf("version %d gaps %+v", rc.Version, rc.Gaps)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, dayID, "summary.md")); !os.IsNotExist(err) {
		t.Errorf("summary.md kept: %v", err)
	}
	if hits := e.Search("zebrapassword", false, 10); len(hits) != 0 {
		t.Errorf("still found: %v", hits)
	}
	if d, _ := e.Get(KindRecording, dayID, 0); d.Summary != "" || strings.Contains(string(d.Recap), "zebrapassword") {
		t.Errorf("detail %q %s", d.Summary, d.Recap)
	}
	r := recordingNow(t, e, dayID)
	if r.RecapState != MinutesPending || r.RecapDue == nil || r.RecapDue.Reason != RecapDueRepair || r.RecapDue.Base != 5 ||
		r.RecapVersion != 2 {
		t.Fatalf("after the repair: %q %+v", r.RecapState, r.RecapDue)
	}
	// Another PUT while that recap is still due moves it to the new base, its reason kept; one that restores Bo's line
	// has the action back, with its to-do.
	restore := dayUpload(6, "早上开会", "Bo 负责发布", "", "")
	restore.Coverage = repair.Coverage
	mustPut(t, e, dayID, restore, now)
	r = recordingNow(t, e, dayID)
	if r.RecapDue == nil || r.RecapDue.Reason != RecapDueRepair || r.RecapDue.Base != 6 || r.RecapDue.N != 2 {
		t.Fatalf("rebased: %+v", r.RecapDue)
	}
	f.merge = func(string) (string, error) {
		return `{"summary": "早上开了会。", "timeline": [{"from_line": 1, "to_line": 2, "kind": "meeting", "title": "早会",
		  "points": [{"text": "早上开会", "line": 1}]}], "actions": [{"who": "Bo", "what": "负责发布", "line": 2}]}`, nil
	}
	e.WriteDayRecaps(context.Background(), 1)
	rc, _ = e.ReadDayRecap(dayID)
	if rc.Version != 3 || rc.Summary == "" || len(rc.Actions) != 1 || rc.Actions[0].TodoID != "todo_bo" {
		t.Errorf("rewritten %+v", rc)
	}
	if len(told) != 0 {
		t.Errorf("a repair before the recap time pushed: %v", told)
	}
}

func writeTestRecapWith(t *testing.T, e *Engine, id string, m Model) {
	t.Helper()
	old := e.Model
	e.Model = m
	defer func() { e.Model = old }()
	if err := e.AskMinutes(id); err != nil {
		t.Fatal(err)
	}
	e.WriteDayRecaps(context.Background(), 5)
	if r := recordingNow(t, e, id); r.RecapState != MinutesReady {
		t.Fatalf("recap state %q", r.RecapState)
	}
}

// Past recording_days a day recording with a recap keeps its metadata and recap (not the notes, transcript or
// summary.md): still read, searched and confirmable; past a finite minutes_days it goes.
func TestDayRecapTrimmed(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	e.MinutesDaysFunc = func() int { return 30 }
	u := dayUpload(1, "Bo 负责发布 quokkaword", "早上开会")
	u.Coverage.To = 3600
	mustPut(t, e, dayID, u, now)
	e.WriteDayRecaps(context.Background(), 1) // the notes
	writeTestRecapWith(t, e, dayID, f)
	if _, err := os.Stat(e.Store.Path(KindRecording, dayID, recapNotesFile)); err != nil {
		t.Fatal(err)
	}
	now = dayClock.AddDate(0, 0, 20)
	e.PruneRecordings(now)
	names := recordingFileNames(t, e, dayID)
	if !slices.Equal(names, []string{"meta.json", "recap.json", "recap.md"}) {
		t.Errorf("kept %v", names)
	}
	r := recordingNow(t, e, dayID)
	if !r.Trimmed() || r.RecapState != MinutesReady {
		t.Errorf("trimmed %+v", r)
	}
	d, err := e.Get(KindRecording, dayID, 100)
	if err != nil || d.Recap == nil || d.Summary == "" || len(d.Segments) != 0 {
		t.Errorf("detail %v %s %q", err, d.Recap, d.Summary)
	}
	hits := e.Search("quokkaword", false, 10)
	if len(hits) == 0 || slices.ContainsFunc(hits, func(h Hit) bool { return !strings.HasSuffix(h.Path, "/recap.md") }) {
		t.Errorf("search %v", hits)
	}
	rc, _ := e.ReadDayRecap(dayID)
	if _, err := e.SetDayRecapTodos(dayID, rc.Version, map[string]string{"a1": "todo_1"}, nil); err != nil {
		t.Errorf("confirm: %v", err)
	}
	if e.WriteDayRecaps(context.Background(), 1); recordingNow(t, e, dayID).RecapVersion != rc.Version {
		t.Error("a trimmed recording got another recap")
	}
	e.PruneRecordings(dayClock.AddDate(0, 0, 40))
	if _, ok := e.Store.Recording(dayID); ok {
		t.Error("kept past minutes_days")
	}
}

// A day recording is never a meeting: not matched to the calendar item it overlaps, not named after it, no minutes,
// no summary of its own (it would read the whole day), even complete.
func TestDayRecordingNeverMeeting(t *testing.T) {
	now := dayClock.Add(3 * time.Hour)
	f := &dayFake{}
	e := dayEngine(t, f, &now)
	_ = e.Store.PutItem(&Item{Kind: KindEvent, ID: "e1", Title: "周会", At: dayClock.Add(5 * time.Minute)})
	u := dayUpload(1, "大家好", "开会了")
	u.Complete, u.Duration = true, 2*3600
	mustPut(t, e, dayID, u, now)
	if n := e.WriteMinutes(context.Background(), 5); n != 0 {
		t.Errorf("minutes: %d", n)
	}
	if n := e.SummarizeRecordings(context.Background(), 5); n != 0 {
		t.Errorf("summarized: %d", n)
	}
	r := recordingNow(t, e, dayID)
	if r.Meeting != nil || r.MinutesState != "" || r.Name != "全天录音 2026-11-11" || r.MinutesAsked || len(r.Items) != 0 {
		t.Errorf("a meeting: %+v", r)
	}
	if r.isMeeting() || r.IsMeeting() {
		t.Error("a meeting by isMeeting or IsMeeting")
	}
	if notes, merges := f.counts(); notes != 0 || merges != 0 {
		t.Errorf("model calls %d %d", notes, merges)
	}
	if _, err := e.Store.ReadMinutes(dayID); err == nil {
		t.Error("minutes of a day recording")
	}
}
