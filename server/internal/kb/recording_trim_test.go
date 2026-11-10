package kb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// trimModel fails the test when called: nothing is summarized or made of a trimmed recording.
type trimModel struct{ t *testing.T }

func (m trimModel) JSONMax(context.Context, string, string, int, any) error {
	m.t.Error("a model call for a trimmed recording")
	return errors.New("no call expected")
}

func recordingFileNames(t *testing.T, e *Engine, id string) []string {
	t.Helper()
	files, err := os.ReadDir(e.Store.Path(KindRecording, id))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	return names
}

// Past recording_days a recording with minutes keeps only its metadata and minutes until minutes_days; one without
// minutes, or past minutes_days, goes as before. Later passes change nothing more.
func TestRecordingTrimAtRecordingDays(t *testing.T) {
	e := goldenEngine(t, nil)
	days, minutesDays := 14, 90
	e.RecordingDaysFunc = func() int { return days }
	e.MinutesDaysFunc = func() int { return minutesDays }
	var indexed, unindexed []string
	e.Index = func(kind, id, _ string, _ Tagging, _ string, _ time.Time) { indexed = append(indexed, id) }
	e.Unindex = func(kind string, ids []string) { unindexed = append(unindexed, ids...) }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	const kept, plain, ancient = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"

	// A meeting Persona attended, with its minutes, summary, live log, crash debris and a pending undo journal.
	u := meetingUpload(1, start, "orchardword transcript line", "the second line")
	u.Attendance = AttendanceListener
	putRecording(t, e, kept, u, start)
	if _, _, err := e.LiveAppend(kept, 0, []LiveSegment{{Start: 1, End: 2, Text: "live line"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(KindRecording, kept, "summary from the transcript"); err != nil {
		t.Fatal(err)
	}
	putMeetingMinutes(t, e, kept, Minutes{Version: 1, Revision: 1, GeneratedAt: start, Title: "Design review", When: "2026-10-31 12:00–12:01",
		Actions: []MinutesAction{{ID: "a1", What: "Write the plan", At: 0}}, Markdown: "# Design review\n\n- quasarminutes decision"})
	if err := os.WriteFile(e.Store.Path(KindRecording, kept, "segments.json.tmp"), []byte("debris"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.Store.recordingDiskSnapshot(kept)
	if err != nil {
		t.Fatal(err)
	}
	journal, _ := json.Marshal(recordingUndo{Version: 1, ID: kept, Files: snapshot})
	if err := os.WriteFile(e.Store.Path(KindRecording, kept, recordingJournal), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	// One with no minutes, one with minutes past minutes_days.
	putRecording(t, e, plain, recUpload(1, start, true, "no minutes"), start)
	old := now.AddDate(0, 0, -minutesDays-1)
	putRecording(t, e, ancient, meetingUpload(1, old, "long ago"), old)
	putMeetingMinutes(t, e, ancient, Minutes{Version: 1, Revision: 1, Title: "Old", Markdown: "# Old"})
	indexed = nil

	if n := e.PruneRecordings(now); n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}
	for _, id := range []string{plain, ancient} {
		if _, err := os.Stat(e.Store.Path(KindRecording, id)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", id, err)
		}
	}
	slices.Sort(unindexed)
	if !slices.Equal(unindexed, []string{plain, ancient}) {
		t.Errorf("unindexed %v", unindexed)
	}
	if names := recordingFileNames(t, e, kept); !slices.Equal(names, []string{"meta.json", "minutes.json", "minutes.md"}) {
		t.Fatalf("a trimmed recording keeps %v", names)
	}
	r := recordingNow(t, e, kept)
	if r.TrimmedAt == nil || !r.TrimmedAt.Equal(now) || r.MinutesState != MinutesReady || r.Attendance != "" || r.Revision != 1 || r.Kind != RecordingMeeting {
		t.Fatalf("trimmed meta %+v", r)
	}
	if !slices.Equal(indexed, []string{kept}) {
		t.Errorf("indexed %v, want the trimmed one again", indexed)
	}

	// Idempotent: nothing more to do.
	indexed = nil
	if n := e.PruneRecordings(now.Add(time.Hour)); n != 0 || len(indexed) != 0 {
		t.Fatalf("again: pruned %d, indexed %v", n, indexed)
	}
	if again := recordingNow(t, e, kept); !again.TrimmedAt.Equal(now) {
		t.Fatalf("trimmed_at moved: %v", again.TrimmedAt)
	}
	if got := e.AttendingRecordings(); len(got) != 0 {
		t.Fatalf("a trimmed recording still attends: %+v", got)
	}

	// Past minutes_days it goes too.
	if n := e.PruneRecordings(start.AddDate(0, 0, minutesDays).Add(time.Second)); n != 1 {
		t.Fatalf("at minutes_days: pruned %d, want 1", n)
	}
	if _, ok := e.Store.Recording(kept); ok {
		t.Fatal("kept past minutes_days")
	}
}

// What a trimmed recording still does: read with its minutes and no transcript, found by its minutes, listed for the
// phone, its action items confirmed, its minutes given to later meetings; and what it doesn't: take an upload, make
// minutes again, get a summary.
func TestRecordingTrimmedReadAndRefused(t *testing.T) {
	e := goldenEngine(t, trimModel{t})
	days := 14
	e.RecordingDaysFunc = func() int { return days }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	putRecording(t, e, recID, meetingUpload(1, start, "orchardword transcript line"), start)
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "Design review", When: "2026-10-31 12:00–12:01",
		Decisions: []MinutesItem{{Text: "Keep the cap", At: 0}}, Actions: []MinutesAction{{ID: "a1", What: "Write the plan", At: 0}},
		Markdown: "# Design review\n\n- quasarminutes decision"})
	changeRecording(t, e, recID, func(r *Recording) { r.MinutesRev, r.MinutesVersion = 1, 1 })
	if n := e.PruneRecordings(now); n != 0 {
		t.Fatalf("pruned %d", n)
	}
	d, err := e.Get(KindRecording, recID, 12000)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		TrimmedAt *time.Time `json:"trimmed_at"`
	}
	if json.Unmarshal(d.Meta, &meta) != nil || meta.TrimmedAt == nil || len(d.Segments) != 0 || d.Recent != "" || d.Summary != "" ||
		!strings.Contains(string(d.Minutes), "Keep the cap") {
		t.Fatalf("detail %+v", d)
	}
	if raw, _ := json.Marshal(d); strings.Contains(string(raw), `"segments":[`) || strings.Contains(string(raw), "orchardword") {
		t.Fatalf("detail shows the transcript: %s", raw)
	}
	if res := e.Find("quasarminutes", 10); len(res) != 1 || res[0].Entry.ID != recID || !strings.HasSuffix(res[0].Hits[0].Path, "/minutes.md") {
		t.Fatalf("find the minutes: %+v", res)
	}
	if res := e.Find("orchardword", 10); len(res) != 0 {
		t.Fatalf("the transcript is still found: %+v", res)
	}
	kept, _ := e.RecordingStates()
	if len(kept) != 1 || kept[0].ID != recID || kept[0].TrimmedAt == nil || kept[0].Revision != 1 || kept[0].MinutesState != MinutesReady {
		t.Fatalf("states %+v", kept)
	}

	// Refused: an upload (expired, also once recording_days covers it again), minutes made again.
	for _, n := range []int{14, 60} {
		days = n
		if st, err := e.PutRecording(recID, meetingUpload(2, start, "a new transcript"), now); err != nil || st.Status != RecordingExpired {
			t.Fatalf("days %d: upload %+v %v, want expired", n, st, err)
		}
	}
	if r := recordingNow(t, e, recID); r.Revision != 1 || r.TrimmedAt == nil {
		t.Fatalf("an upload changed it: %+v", r)
	}
	if err := e.AskMinutes(recID); !errors.Is(err, ErrTranscriptExpired) {
		t.Fatalf("ask minutes: %v", err)
	}
	if n := e.SummarizeRecordings(context.Background(), 3); n != 0 {
		t.Fatalf("summarized %d", n)
	}
	if n := e.WriteMinutes(context.Background(), 2); n != 0 {
		t.Fatalf("wrote minutes %d", n)
	}
	if m, err := e.SetMinutesTodos(recID, 1, map[string]string{"a1": "todo_x"}, nil); err != nil || m.Actions[0].TodoID != "todo_x" {
		t.Fatalf("confirming a to-do: %+v %v", m, err)
	}

	// A later meeting draws on its minutes.
	later := "44444444-4444-4444-8444-444444444444"
	putRecording(t, e, later, attendedUpload(1, now, AttendanceListener), now)
	material, err := e.MeetingMaterial(later, "What did the design review decide?")
	if err != nil || len(material.Earlier) != 1 || material.Earlier[0].ID != recID || material.Earlier[0].Minutes.Decisions[0].Text != "Keep the cap" {
		t.Fatalf("material %+v %v", material, err)
	}

	// The user's delete still removes everything and leaves a tombstone.
	if ok, err := e.DeleteRecording(recID, now); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID)); !os.IsNotExist(err) {
		t.Fatalf("files left: %v", err)
	}
	if _, deleted := e.RecordingStates(); !slices.Equal(deleted, []string{recID}) {
		t.Fatalf("tombstones %v", deleted)
	}
}

// minutes_days is never below recording_days, and 365 without a setting.
func TestMinutesDaysEffective(t *testing.T) {
	e := goldenEngine(t, nil)
	if got := e.MinutesDays(); got != DefaultMinutesDays {
		t.Fatalf("default %d", got)
	}
	days, minutes := 30, 7
	e.RecordingDaysFunc = func() int { return days }
	e.MinutesDaysFunc = func() int { return minutes }
	if got := e.MinutesDays(); got != 30 {
		t.Fatalf("below recording_days: %d, want 30", got)
	}
	minutes = 3650
	if got := e.MinutesDays(); got != 3650 {
		t.Fatalf("%d, want 3650", got)
	}
	if got := MinutesDaysFor(0, 0); got != DefaultMinutesDays {
		t.Fatalf("MinutesDaysFor(0, 0) = %d", got)
	}
}
