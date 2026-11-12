package assistant

import (
	"context"
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
	// The span assumed for the hold is not the phone's: meeting.json's timeline (the final pass's) never has it.
	m, _, _ := h.engine().MeetingRead(meetingRec)
	if tl := m.Transcription.Timeline; tl != nil && len(tl.Speaking) > 0 {
		t.Fatalf("an assumed span in the timeline: %+v", tl.Speaking)
	}
}

// A speaking off lost with a dropped socket: the next socket's start.timeline (or an upload's timeline) closes the
// open span, and the sentences after it go out at once instead of waiting echoHoldMost; the timeline keeps the
// phone's span.
func TestHubEchoSpanRepairedByTheTimeline(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) { f.live = jobScript{lines: mixedLines} })
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(1.6)
	p.wait("line", 1)
	p.send(map[string]any{"type": "speaking", "on": true, "at": 1.65})
	waitFor(t, "the span open", func() bool {
		r := h.s.hubRunOf(meetingRec)
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.openFrom != nil
	})
	_ = p.c.CloseNow() // the speaking off at 1.7 is lost with it
	waitFor(t, "the socket gone", func() bool { return !h.s.hubRunOf(meetingRec).socketOpen() })
	back, ready := h.open(meetingRec, 0, map[string]any{"speaking": [][]float64{{1.65, 1.7}}})
	if ready["from"] != 1.6 {
		t.Fatalf("ready %v", ready)
	}
	back.audio(3)
	back.wait("line", 2)
	if got := back.texts(); !slices.Equal(got, []string{mixedLines[1].text, mixedLines[2].text}) {
		t.Fatalf("lines %q", got)
	}
	m, _, _ := h.engine().MeetingRead(meetingRec)
	if tl := m.Transcription.Timeline; tl == nil || len(tl.Speaking) != 1 || tl.Speaking[0] != [2]float64{1.65, 1.7} {
		t.Fatalf("the timeline: %+v", m.Transcription.Timeline)
	}

	// An upload's timeline repairs it too.
	back.send(map[string]any{"type": "speaking", "on": true, "at": 4.7})
	waitFor(t, "the span open", func() bool {
		r := h.s.hubRunOf(meetingRec)
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.openFrom != nil
	})
	h.hubUpload(meetingRec, 2, map[string]any{"timeline": map[string]any{"speaking": [][]float64{{1.65, 1.7}, {4.7, 4.8}}}})
	r := h.s.hubRunOf(meetingRec)
	r.mu.Lock()
	open := r.openFrom
	r.mu.Unlock()
	if open != nil {
		t.Fatalf("still open from %v after the upload closed it", *open)
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

// A heard ask that reaches the hub before its audio flushes once the audio reaches its at, not after askLiveWait.
func TestHubHeardAskBeforeItsAudio(t *testing.T) {
	h := newHubFixture(t)
	h.tr.set(func(f *fakeTranscriber) {
		f.live = jobScript{lines: []fakeLine{{0.5, 1.5, "What did we decide about the cap?"}, {30, 31, "Later."}}, held: true}
	})
	h.hubUpload(meetingRec, 1, nil)
	p, _ := h.open(meetingRec, 0, nil)
	p.audio(1)
	waitFor(t, "audio", func() bool {
		acks := p.of("ack")
		return len(acks) > 0 && acks[len(acks)-1]["at"] == 1.0
	})
	began := time.Now()
	asked := make(chan map[string]any, 1)
	go func() {
		asked <- h.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "at": 1.6}, http.StatusOK)
	}()
	waitFor(t, "the ask waiting", func() bool { return len(h.asksOf(meetingRec)) == 1 })
	p.audio(1) // the ask's audio comes after it
	ask := meetingAskOf(t, <-asked)
	if ask["state"] != askAnswered {
		t.Fatalf("heard ask: %v", ask)
	}
	if took := time.Since(began); took >= askLiveWait {
		t.Fatalf("the ask waited %v for audio that came at once", took)
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

// attachedPhone waits until recording id's run has a socket other than before (a new one attached, not started).
func (h *hubFixture) attachedPhone(id string, before *phoneSocket) *phoneSocket {
	h.t.Helper()
	var p *phoneSocket
	waitFor(h.t, "the socket attached", func() bool {
		r := h.s.hubRunOf(id)
		if r == nil {
			return false
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		p = r.phone
		return p != nil && p != before
	})
	return p
}

// A socket gets nothing before ready: a problem that changes while it waits for its start goes out once, after ready.
func TestHubNothingBeforeReady(t *testing.T) {
	h := newHubFixture(t)
	h.hubUpload(meetingRec, 1, nil)
	p := dialPhone(t, h.sockets, meetingRec)
	h.attachedPhone(meetingRec, nil)
	r := h.s.hubRunOf(meetingRec)
	r.mu.Lock()
	r.setUpstreamLocked(problemUpstreamFailed)
	r.mu.Unlock()
	p.send(map[string]any{"type": "start", "at": 0})
	p.wait("problem", 1)
	p.mu.Lock()
	frames := slices.Clone(p.frames)
	p.mu.Unlock()
	if len(frames) != 2 || frames[0]["type"] != "ready" || frames[1]["type"] != "problem" || frames[1]["code"] != problemUpstreamFailed {
		t.Fatalf("frames %v", frames)
	}
}

// A socket that never sends start counts as down: the fallback comes fallbackAfter after the first hub-mode upload
// while it waits for its start, and its end leaves the time the socket is down from as it was.
func TestHubSocketNeverStartedCountsAsDown(t *testing.T) {
	h := newHubFixture(t)
	h.hubUpload(meetingRec, 1, map[string]any{"segments": phoneSegs(fakeLine{0, 3, "Phone line one."})})
	r := h.s.hubRunOf(meetingRec)
	r.mu.Lock()
	since := r.downSince
	r.mu.Unlock()
	p := dialPhone(t, h.sockets, meetingRec)
	sock := h.attachedPhone(meetingRec, nil)
	waitFor(t, "the fallback", func() bool { return h.transcription(meetingRec)["fallback"] == true })
	r.mu.Lock()
	still := r.phone == sock
	r.mu.Unlock()
	if !still {
		t.Fatal("the fallback waited for the socket that never started to end")
	}
	_ = p.c.CloseNow()
	waitFor(t, "the socket gone", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.phone == nil
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.downSince.Equal(since) {
		t.Fatalf("down since %v, was %v", r.downSince, since)
	}
}

// A ready hub stays ready while its readiness is probed again: neither an unrelated settings change nor a registry
// change during the probe makes the first upload naming the hub fall back to the device; and with no answer standing
// (the choice just changed) the upload waits for the probe.
func TestHubModeChosenWhileReadinessIsProbed(t *testing.T) {
	h := newHubFixture(t)
	if !h.s.transcriptionReadyNow() {
		t.Fatal("not ready")
	}
	setSettings(t, h.s, func(v *Settings) { days := 30; v.RecordingDays = &days })
	started, release := make(chan struct{}, 1), make(chan struct{})
	h.tr.set(func(f *fakeTranscriber) {
		f.infoHook = func() {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		}
	})
	h.s.transcriptionReadinessChanged() // a registry change: probed again, in flight below
	probed := make(chan struct{})
	go func() { defer close(probed); h.s.refreshTranscriptionReady(context.Background()) }()
	<-started
	if out := h.hubUpload(meetingRec, 1, nil); out["transcription"] != "hub" {
		t.Fatalf("during the probe: %v", out)
	}
	close(release)
	<-probed

	// Another contribution chosen: no answer stands; the upload waits for the probe and takes its answer.
	gate := make(chan struct{})
	h.tr.set(func(f *fakeTranscriber) { f.infoHook = func() { <-gate } })
	h.s.transcriptionReady.mu.Lock()
	h.s.transcriptionReady.resetLocked("another")
	h.s.transcriptionReady.mu.Unlock()
	go func() { h.s.refreshTranscriptionReady(context.Background()) }()
	uploaded := make(chan map[string]any, 1)
	go func() { uploaded <- h.hubUpload(meetingOther, 1, nil) }()
	close(gate)
	if out := <-uploaded; out["transcription"] != "hub" {
		t.Fatalf("with no answer standing: %v", out)
	}
}
