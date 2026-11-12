package assistant

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/kb"
)

// The hub's live sentences (docs/assistant/meeting-minutes.md "Audio from the phone"): lines in order, the role, the
// echo hold-back, captions, asks, the job's life. Names and texts are placeholders; the sentences mix Chinese and
// English as people speak (written as spoken, never translated).

// mixedLines are sentences as a meeting says them: Chinese, English, and both in one sentence.
var mixedLines = []fakeLine{
	{0.5, 1.5, "我们这周先把 export cap 保持在 4 MB。"}, // i18n-ignore
	{2.0, 3.0, "Bo owns the vendor follow-up."},
	{3.5, 4.5, "下周三之前 Alice 要把 pricing 的 proposal 发出来。"}, // i18n-ignore
}

// The job's sentences become line frames and live lines in order, with contiguous positions and source hub, and the
// hub's transcript; captions go out as interim frames; GET …/live has the lines with their source and the
// transcription.
func TestHubLines(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines, interim: true} })
	h.hubUpload(meetingRec, 1, map[string]any{"title": "Design review",
		"calendar": map[string]any{"title": "Weekly sync", "attendees": []string{"Alice", "Bo", "alice"}}})
	setSettings(t, h.s, func(v *Settings) { g := []string{"export cap", "Bo"}; v.MeetingGlossary = &g })
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(5)
	p.wait("line", 3)
	if got := p.texts(); !slices.Equal(got, []string{mixedLines[0].text, mixedLines[1].text, mixedLines[2].text}) {
		t.Fatalf("lines %q", got)
	}
	for i, f := range p.of("line") {
		if f["n"] != float64(i) {
			t.Fatalf("line %d: %v", i, f)
		}
	}
	if len(p.of("interim")) != 3 {
		t.Fatalf("interim frames: %v", p.of("interim"))
	}
	segs := h.lines(meetingRec)
	if len(segs) != 3 || segs[0].Source != kb.SourceHub || segs[2].Text != mixedLines[2].text {
		t.Fatalf("live log: %+v", segs)
	}
	out := h.do("GET", livePath(meetingRec)+"?from=0", "", nil, http.StatusOK)
	lines := out["lines"].([]any)
	if len(lines) != 3 || lines[1].(map[string]any)["source"] != "hub" || out["transcription"].(map[string]any)["mode"] != "hub" {
		t.Fatalf("GET …/live: %v", out)
	}
	waitFor(t, "the hub's transcript", func() bool { return len(h.hubTexts(meetingRec)) == 3 })
	jobs := h.tr.jobsOf(extwire.TranscriptionLive)
	if len(jobs) != 1 {
		t.Fatalf("%d live jobs", len(jobs))
	}
	start := jobs[0].start
	if start.Language != "en-US" || *start.At != 0 || !slices.Equal(start.Glossary, []string{"Design review", "Weekly sync", "Alice", "Bo", "export cap"}) {
		t.Fatalf("the job's start: %+v", start)
	}
	if _, err := h.engine().HubTranscript(meetingRec); err != nil {
		t.Fatal(err)
	}
	// The phone's own live lines are refused: the live log is the hub's.
	wantCode(t, h.do("POST", livePath(meetingRec), "", `{"from":3,"segments":[{"start":5,"end":6,"text":"x"}]}`, http.StatusConflict), codeHubTranscribes)
}

// A sentence that ends while the role is off joins the hub's transcript and sends no line, taking no position.
func TestHubRoleOff(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines} })
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, map[string]any{"roles": []map[string]any{{"at": 0, "attendance": "listener"}}})
	p.send(map[string]any{"type": "role", "at": 1.8, "attendance": "off"})
	p.send(map[string]any{"type": "role", "at": 3.2, "attendance": "listener"})
	p.audio(5)
	p.wait("line", 2)
	waitFor(t, "the hub's transcript", func() bool { return len(h.hubTexts(meetingRec)) == 3 })
	if got := lineTexts(h.lines(meetingRec)); !slices.Equal(got, []string{mixedLines[0].text, mixedLines[2].text}) {
		t.Fatalf("live log %q", got)
	}
	if f := p.of("line"); len(f) != 2 || f[1]["n"] != 1.0 {
		t.Fatalf("lines %v", f)
	}
	m, _, _ := h.engine().MeetingRead(meetingRec)
	if tl := m.Transcription.Timeline; tl == nil || len(tl.Roles) != 3 {
		t.Fatalf("the timeline: %+v", m.Transcription)
	}
}

// While Persona speaks a sentence that may be its own voice waits (and those after it, in order); when the span
// closes the echo is dropped everywhere and the rest is kept; no caption goes out meanwhile.
func TestHubEchoHoldBack(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines, interim: true} })
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(1.6)
	p.wait("line", 1)
	p.send(map[string]any{"type": "speaking", "on": true, "at": 1.8})
	p.audio(3)
	waitFor(t, "both sentences held", func() bool {
		r := h.s.hubRunOf(meetingRec)
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.held) == 2
	})
	if len(p.of("line")) != 1 || len(p.of("interim")) != 1 {
		t.Fatalf("frames while Persona speaks: %v", p.frames)
	}
	p.send(map[string]any{"type": "speaking", "on": false, "at": 3.2})
	p.wait("line", 2)
	if got := p.texts(); !slices.Equal(got, []string{mixedLines[0].text, mixedLines[2].text}) {
		t.Fatalf("lines %q", got)
	}
	waitFor(t, "the hub's transcript", func() bool { return len(h.hubTexts(meetingRec)) == 2 })
	if got := h.hubTexts(meetingRec); slices.Contains(got, mixedLines[1].text) {
		t.Fatalf("the echo in the hub's transcript: %q", got)
	}
}

// A span the phone never closes counts as closed after echoHoldMost.
func TestHubEchoSpanTimesOut(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines[:2]} })
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	echoHoldMost = 200 * time.Millisecond
	p.send(map[string]any{"type": "speaking", "on": true, "at": 1.9})
	p.audio(3.2)
	p.wait("line", 1)
	waitFor(t, "the span closed", func() bool {
		r := h.s.hubRunOf(meetingRec)
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.openFrom == nil && len(r.held) == 0
	})
	if got := p.texts(); len(got) != 1 {
		t.Fatalf("lines %q", got) // [2, 3] is inside [1.9, 2.1] widened by half a second: dropped
	}
}

// GET …/live has the caption while it is fresh and the role is not off; ?parts=interim and ?parts=transcription
// return them with their revisions; meeting_live tells of lines, interim and transcription.
func TestHubInterimAndEvents(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) {
		f.live = jobScript{lines: mixedLines[:1], interim: true, features: []string{extwire.TranscriptionLive, extwire.TranscriptionReplay}}
	})
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(2)
	p.wait("line", 1)
	p.wait("problem", 2) // no_interim from the job's ready without captions, then null once they came
	if f := p.of("problem"); len(f) != 2 || f[0]["code"] != problemNoInterim || f[1]["code"] != nil {
		t.Fatalf("problems: %v (captions came)", f)
	}
	out := h.do("GET", livePath(meetingRec)+"?parts=interim,transcription", "", nil, http.StatusOK)
	in, _ := out["interim"].(map[string]any)
	revs := out["revs"].(map[string]any)
	if in == nil || in["text"] != mixedLines[0].text || revs["interim"] == nil || revs["transcription"] == nil || out["transcription"] == nil {
		t.Fatalf("parts: %v", out)
	}
	waitFor(t, "meeting_live events", func() bool {
		var parts []string
		for _, ev := range h.f.liveEventsOf(meetingRec) {
			for _, p := range ev["changed"].([]any) {
				parts = append(parts, p.(string))
			}
		}
		return slices.Contains(parts, kb.PartLines) && slices.Contains(parts, kb.PartInterim) && slices.Contains(parts, kb.PartTranscription)
	})
	interimShown = 0 // a caption older than interimShown is not shown
	if out := h.do("GET", livePath(meetingRec), "", nil, http.StatusOK); out["interim"] != nil {
		t.Fatalf("an old caption: %v", out["interim"])
	}
	interimShown = time.Minute
	p.send(map[string]any{"type": "role", "at": 0, "attendance": "off"})
	waitFor(t, "no interim while off", func() bool {
		return h.do("GET", livePath(meetingRec), "", nil, http.StatusOK)["interim"] == nil
	})
}

// A heard ask in hub mode ignores live_next: the hub's sentences up to at are flushed first.
func TestHubHeardAskFlushes(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) {
		f.live = jobScript{lines: []fakeLine{{0.5, 1.5, "What did we decide about the cap?"}, {30, 31, "Later."}}, held: true}
	})
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(1.6) // its window is still open: only the flush sends the sentence
	waitFor(t, "audio", func() bool {
		acks := p.of("ack")
		return len(acks) > 0 && acks[len(acks)-1]["at"] == 1.6
	})
	if len(p.of("line")) != 0 {
		t.Fatal("a line before the flush")
	}
	ask := meetingAskOf(t, h.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "at": 1.6, "live_next": 50}, http.StatusOK))
	if ask["state"] != askAnswered {
		t.Fatalf("heard ask: %v", ask)
	}
	frames, _, _, _ := h.tr.jobsOf(extwire.TranscriptionLive)[0].snapshot()
	if !slices.ContainsFunc(frames, func(f extwire.TranscriptionFrame) bool { return f.Type == extwire.TFFlush }) {
		t.Fatalf("no flush: %+v", frames)
	}
	calls := h.model.called()
	if len(calls) == 0 || !strings.Contains(calls[len(calls)-1].User, "What did we decide about the cap?") {
		t.Fatalf("the model's input: %v", calls)
	}
}

// A dropped job socket is opened again at once from the live edge; a job with no audio for jobIdle is ended.
func TestHubJobReopenedAndIdle(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines, dropAt: 2.5} })
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(2.6)
	p.wait("line", 1)
	waitFor(t, "the job dropped", func() bool {
		r := h.s.hubRunOf(meetingRec)
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.job == nil && r.dropped
	})
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: []fakeLine{{3.0, 3.5, "Back again."}}} })
	jobIdle = 300 * time.Millisecond
	p.audio(1)
	p.wait("line", 2)
	jobs := h.tr.jobsOf(extwire.TranscriptionLive)
	if len(jobs) != 2 || *jobs[1].start.At < 2.5 {
		t.Fatalf("jobs: %d, the second from %v", len(jobs), jobs[len(jobs)-1].start.At)
	}
	waitFor(t, "the idle job ended", func() bool {
		_, _, _, ended := jobs[1].snapshot()
		return ended
	})
	p.audio(0.5) // audio again: a job again
	waitFor(t, "a third job", func() bool { return len(h.tr.jobsOf(extwire.TranscriptionLive)) == 3 })
	if f := p.of("problem"); len(f) != 0 {
		t.Fatalf("a drop reopened at once is no problem: %v", f)
	}
}

// The job's usage is logged with the recording id and kept with the transcription.
func TestHubUsage(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines[:1], usage: 42} })
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(2)
	p.send(map[string]any{"type": "end", "at": 2.0})
	p.waitClosed()
	waitFor(t, "the usage kept", func() bool {
		m, _, _ := h.engine().MeetingRead(meetingRec)
		return m.Transcription.UsageSeconds == 42
	})
	if len(p.texts()) != 1 {
		t.Fatalf("lines %q", p.texts())
	}
}
