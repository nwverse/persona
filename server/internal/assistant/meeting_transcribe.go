package assistant

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
)

// Hub transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "Phase 5: hub transcription"): a meeting uploaded
// with transcription hub streams its audio to the hub (meeting_audio.go), which stores it (kb.AudioWriter) and hands
// it to a transcription contribution's live job (meeting_transcribe_job.go). Each recording being transcribed has a
// hubRun: it holds a sentence back while Persona speaks and keeps or drops it by the echo rule (kb.EchoHeld,
// kb.EchoOf), puts the kept ones in the hub's transcript (hub_segments.json, kb.HubTranscriptPut) and, while the role
// at their end is not off, in the live log (kb.LiveAppendHub), sending the phone its line frames. When the hub has no
// sentences the phone's text fills the live log (meeting_transcribe_fallback.go). After the meeting the final pass
// transcribes the stored audio again (meeting_transcribe_final.go). Nothing here logs what was said.

// The times of hub transcription; tests shorten them.
var (
	audioStartWait   = 10 * time.Second // the phone's start frame comes within this
	audioAckEvery    = 5 * time.Second  // the hub acknowledges the audio it stored this often
	audioPingEvery   = 30 * time.Second // the hub pings a socket this often; one that answers nothing is closed
	audioEndWait     = 15 * time.Second // after the phone's end, its last lines come within this
	fallbackAfter    = 30 * time.Second // the socket down, or a problem standing, this long: the phone's text fills
	jobRetry         = time.Minute      // a job that could not be opened, or failed, is opened again after this
	jobReadyWait     = 30 * time.Second // a job answers ready within this, else it counts as upstream_unavailable
	jobIdle          = time.Minute      // a job with no audio this long is ended (sessions bill silence)
	jobDoneWait      = 5 * time.Minute  // a replay job sends done within this of the manager's end
	echoHoldMost     = time.Minute      // a speaking span still open after this counts as closed
	hubSegmentsEvery = 15 * time.Second // the hub's live sentences are written to hub_segments.json this often
	hubTick          = 250 * time.Millisecond
	interimShown     = 10 * time.Second // GET …/live shows a caption at most this old
	finalEvery       = time.Minute      // the final passes are looked at this often (and when kicked)
	finalGapWait     = 30 * time.Minute // the final pass waits at most this long after the complete upload for gaps
	finalRetries     = []time.Duration{time.Minute, 5 * time.Minute}
	// The most audio sent ahead of a job's last ack: live (more is skipped, the job moved on with seek) and replay.
	liveAhead, replayAhead = 30 * time.Second, time.Minute
)

// liveInterim is the latest caption of a meeting the hub transcribes: interim on GET …/live, while it is at most
// interimShown old (At, the manager's clock).
type liveInterim struct {
	Start float64   `json:"start"`
	Text  string    `json:"text"`
	At    time.Time `json:"-"`
}

// hubRuns are the recordings being transcribed live in this process, and their final passes.
type hubRuns struct {
	mu     sync.Mutex
	by     map[string]*hubRun
	passes map[string]*finalPassState
	kick   chan struct{}
	// wg counts the goroutines of hub transcription: the sockets, the runs' clocks and jobs, the final passes.
	wg sync.WaitGroup
}

// hubRun is one recording's live transcription. mu is held while its state changes; the knowledge base is written
// under it (never the other way round: the knowledge base's callbacks never reach a run).
type hubRun struct {
	s    *Service
	id   string
	quit chan struct{}
	ctx  context.Context
	end  context.CancelFunc

	mu       sync.Mutex
	e        *kb.Engine
	stopped  bool
	stopCode string // what the run ended with (a socket's fatal error code, "" for none)

	// The phone's socket (nil while down); started: one sent start; downSince: since when it is down (from the first
	// hub-mode upload while none ever started); ended: the phone sent end.
	phone     *phoneSocket
	started   bool
	downSince time.Time
	ended     bool

	// The live job (nil while none), when one may be opened again, whether the last one dropped.
	job        *liveJob
	jobRetryAt time.Time
	dropped    bool
	// edge is the live edge, in samples: the end of the latest audio given to the live job; audioAt when audio came.
	edge    int64
	audioAt time.Time

	// The timeline: meeting.json's union and what the socket said since; role is the meeting's own (before the
	// timeline's first change). openFrom is a speaking span open now (since openAt), held the sentences waiting for
	// it to close, in order.
	timeline kb.Timeline
	role     string
	openFrom *float64
	openAt   time.Time
	held     []kb.EchoSentence

	// The hub's live sentences (hub_segments.json's and those since), whether hub_segments.json lags behind them,
	// when it was last written, the end of the latest one.
	hub        []kb.RecordingSegment
	hubDirty   bool
	hubPutAt   time.Time
	lastHubEnd float64

	interim    *liveInterim
	interimRev int64

	// Problems: the upstream's (upstream_unavailable, upstream_failed) since upstreamAt, no captions, a job ended by
	// a fatal error; shown is the problem the phone and meeting.json were told last.
	upstream   string
	upstreamAt time.Time
	noInterim  bool
	fatal      bool
	shown      string

	// The fallback (the phone's text fills the live log): on, the spans it filled and the end of the latest; device
	// the latest upload's segments and deviceTo the furthest end of any.
	fallback bool
	filled   [][2]float64
	filledTo float64
	device   []kb.RecordingSegment
	deviceTo float64

	// flushedTo: the job's sentences up to here are in (flushed); wake is closed on every change a waiter reads.
	flushedTo float64
	wake      chan struct{}
}

// The problems of hub transcription (the socket's problem frame, transcription.problem).
const (
	problemUpstreamUnavailable = "upstream_unavailable"
	problemUpstreamFailed      = "upstream_failed"
	problemNoInterim           = "no_interim"
)

// hubRunOf is recording id's run, nil without one.
func (s *Service) hubRunOf(id string) *hubRun {
	id, ok := kb.RecordingID(id)
	if !ok {
		return nil
	}
	s.hubTr.mu.Lock()
	defer s.hubTr.mu.Unlock()
	return s.hubTr.by[id]
}

// hubRunFor is recording id's run, made when there is none; nil when the recording is not transcribed on the hub or
// its live pass is over (the final pass started).
func (s *Service) hubRunFor(e *kb.Engine, id string) *hubRun {
	id, ok := kb.RecordingID(id)
	if !ok {
		return nil
	}
	s.hubTr.mu.Lock()
	if r := s.hubTr.by[id]; r != nil {
		s.hubTr.mu.Unlock()
		r.mu.Lock()
		r.e = e
		r.mu.Unlock()
		return r
	}
	s.hubTr.mu.Unlock()
	r := newHubRun(s, e, id)
	if r == nil {
		return nil
	}
	s.hubTr.mu.Lock()
	if old := s.hubTr.by[id]; old != nil { // made meanwhile
		s.hubTr.mu.Unlock()
		r.end()
		return old
	}
	if s.hubTr.by == nil {
		s.hubTr.by = map[string]*hubRun{}
	}
	s.hubTr.by[id] = r
	s.hubTr.mu.Unlock()
	s.hubTr.wg.Add(1)
	go func() {
		defer s.hubTr.wg.Done()
		r.loop()
	}()
	return r
}

// newHubRun is recording id's run as the knowledge base has it: its timeline, the hub's live sentences, the lines
// the phone's text filled, the standing problem. Nil when it has no live pass.
func newHubRun(s *Service, e *kb.Engine, id string) *hubRun {
	l, rec, err := e.LiveRead(id)
	if err != nil || !rec.HubTranscribed() || rec.Trimmed() {
		return nil
	}
	t := rec.Conv.Transcription
	if !t.FinalFrom.IsZero() || !t.AudioGoneAt.IsZero() || t.State == kb.TranscriptionFinal || t.State == kb.TranscriptionFailed {
		return nil
	}
	h, err := e.HubTranscript(id)
	if err != nil || h.Pass == kb.HubPassFinal {
		return nil
	}
	ctx, cancel := context.WithCancel(s.runCtx())
	r := &hubRun{s: s, id: id, e: e, quit: make(chan struct{}), ctx: ctx, end: cancel, role: rec.AttendanceRole(),
		hub: slices.Clone(h.Segments), wake: make(chan struct{}), shown: t.Problem, fallback: t.Fallback}
	if t.Timeline != nil {
		r.timeline.Merge(*t.Timeline)
	}
	for _, sg := range r.hub {
		r.lastHubEnd = max(r.lastHubEnd, sg.End)
	}
	switch t.Problem {
	case problemNoInterim:
		r.noInterim = true
	case problemUpstreamUnavailable, problemUpstreamFailed:
		r.upstream, r.upstreamAt = t.Problem, time.Now()
	}
	for _, sg := range l.Segments {
		if sg.Source == kb.SourceDevice {
			r.filled = append(r.filled, [2]float64{sg.Start, sg.End})
			r.filledTo = max(r.filledTo, sg.End)
		}
	}
	if segs, err := e.RecordOf(id); err == nil {
		for _, sg := range segs {
			if sg.Source == kb.SourceDevice {
				r.device = append(r.device, sg)
				r.deviceTo = max(r.deviceTo, sg.End)
			}
		}
	}
	return r
}

// loop is the run's clock: spans open too long, the hub's transcript, idle jobs, the fallback.
func (r *hubRun) loop() {
	t := time.NewTicker(hubTick)
	defer t.Stop()
	for {
		select {
		case <-r.quit:
			return
		case now := <-t.C:
			r.tick(now)
		}
	}
}

func (r *hubRun) tick(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if r.openFrom != nil && now.Sub(r.openAt) >= echoHoldMost { // counts as closed then
		from := *r.openFrom
		r.expireSpanLocked(from, from+echoHoldMost.Seconds())
	}
	if r.hubDirty && now.Sub(r.hubPutAt) >= hubSegmentsEvery {
		r.putHubLocked()
	}
	if j := r.job; j != nil && !j.ending && !r.audioAt.IsZero() && now.Sub(r.audioAt) >= jobIdle {
		r.s.logf("recording %s: transcription job idle, ended", r.id)
		r.endJobLocked(j)
	}
	r.fallbackCheckLocked(now)
}

// notifyLocked wakes the waiters (hubFlush, awaitHeard).
func (r *hubRun) notifyLocked() {
	close(r.wake)
	r.wake = make(chan struct{})
}

// phoneSendLocked sends the phone's socket frame v, when one is up.
func (r *hubRun) phoneSendLocked(v any) {
	if r.phone != nil {
		r.phone.send(v)
	}
}

// roleNowLocked is the role at the live edge, for what only goes out while the meeting is attended.
func (r *hubRun) roleNowLocked() string {
	return r.timeline.RoleAt(float64(r.edge)/kb.AudioRate, r.role)
}

// ---- the phone's socket ----

// attach makes p the recording's socket; the one before it is replaced (the fatal error replaced, closed). A run
// ended meanwhile takes none: the code it ended with.
func (r *hubRun) attach(p *phoneSocket) (refused string) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return cmp.Or(r.stopCode, codeTranscriptionFinal)
	}
	old := r.phone
	r.phone = p
	r.mu.Unlock()
	if old != nil && old != p {
		old.fail(codeReplaced)
	}
	return ""
}

// begin is the phone's start on socket p: its timeline joins the union; the socket is up from now (the fallback's
// "down" ends). The standing problem goes out after ready (sent by the caller first).
func (r *hubRun) begin(p *phoneSocket, t *kb.Timeline) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phone != p || r.stopped {
		return
	}
	r.started, r.downSince, r.ended = true, time.Time{}, false
	if t != nil {
		r.mergeTimelineLocked(*t)
	}
	r.reconcileOpenLocked() // a speaking off the socket before lost
	if r.shown != "" {
		p.send(map[string]any{"type": "problem", "code": r.shown})
	}
}

// detach: socket p is over; ended says the phone sent end (else it is down from now).
func (r *hubRun) detach(p *phoneSocket, ended bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phone != p {
		return
	}
	r.phone = nil
	r.clearInterimLocked()
	if ended {
		r.ended = true
		return
	}
	r.downSince = time.Now()
}

// socketOpen: a socket is up that has not sent end.
func (r *hubRun) socketOpen() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phone != nil && !r.ended
}

// audio is audio the phone's socket stored, from recording second at: what lies past the live edge goes to the live
// job (audio behind it, a hole filled late, is only stored, for the final pass).
func (r *hubRun) audio(at float64, pcm []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	from := int64(math.Round(at * kb.AudioRate))
	to := from + int64(len(pcm)/2)
	if to <= r.edge {
		return
	}
	if from < r.edge {
		pcm, from = pcm[(r.edge-from)*2:], r.edge
	}
	r.edge, r.audioAt = to, time.Now()
	if j := r.ensureJobLocked(from); j != nil {
		j.feedLocked(from, pcm)
	}
}

// roleFrame is the socket's role frame: the role from at on.
func (r *hubRun) roleFrame(at float64, attendance string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mergeTimelineLocked(kb.Timeline{Roles: []kb.TimelineRole{{At: at, Attendance: attendance}}})
}

// speaking is the socket's speaking frame: a span opens at at (sentences that end after it began wait), or closes.
func (r *hubRun) speaking(on bool, at float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if on {
		if r.openFrom == nil {
			r.openFrom, r.openAt = &at, time.Now()
			r.clearInterimLocked() // no caption goes out while Persona speaks
		}
		return
	}
	if r.openFrom == nil {
		return // a span the socket never saw open: the uploads' timeline has it
	}
	from := *r.openFrom
	r.closeSpanLocked(from, max(at, from))
}

// closeSpanLocked: the open span is [from, to]; the sentences held for it are judged now.
func (r *hubRun) closeSpanLocked(from, to float64) {
	r.openFrom = nil
	r.mergeTimelineLocked(kb.Timeline{Speaking: [][2]float64{{from, to}}})
	held := r.held
	r.held = nil
	for _, s := range held {
		r.judgeLocked(s)
	}
	r.notifyLocked()
}

// expireSpanLocked: the open span counts as closed at to without the phone saying so (held echoHoldMost, or the run
// ends): the sentences held for it are judged with [from, to] as a speaking span, which is never written to the
// timeline. The timeline has only what the phone said, so a span whose speaking off a dropped socket lost (and that
// the next start or upload repairs) never becomes an assumed span that drops real speech in the final pass.
func (r *hubRun) expireSpanLocked(from, to float64) {
	r.openFrom = nil
	spans := append(kb.EchoSpans(r.timeline), kb.EchoSpan{From: from, To: max(from, to)})
	held := r.held
	r.held = nil
	for _, s := range held {
		if kb.EchoOf(s, spans) == kb.EchoKeep {
			r.acceptLocked(s)
		}
	}
	r.notifyLocked()
}

// reconcileOpenLocked closes the open speaking span once the timeline has it closed: the speaking off a dropped
// socket lost comes back in the next socket's start.timeline or an upload's timeline (a span still open when a socket
// starts is not in its start, the phone sends speaking on after it). The timeline's span that closes it begins at or
// before the open one and ends after it.
func (r *hubRun) reconcileOpenLocked() {
	if r.openFrom == nil {
		return
	}
	from := *r.openFrom
	const slack = 1.0 / kb.AudioRate // times are rounded to a sample
	for _, sp := range r.timeline.Speaking {
		if sp[0] <= from+slack && sp[1] > from+slack {
			r.closeSpanLocked(from, sp[1])
			return
		}
	}
}

// mergeTimelineLocked adds t to the run's timeline and meeting.json's.
func (r *hubRun) mergeTimelineLocked(t kb.Timeline) {
	if !r.timeline.Merge(t) {
		return
	}
	if _, err := r.e.TranscriptionUpdate(r.id, func(m *kb.MeetingTranscription, _ kb.Recording) error {
		var tl kb.Timeline
		if m.Timeline != nil {
			tl = *m.Timeline
		}
		tl.Merge(t)
		m.Timeline = &tl
		return nil
	}); err != nil {
		r.s.logf("recording %s: timeline: %v", r.id, err)
	}
}

// ---- the sentences ----

// sentenceLocked is a sentence of the live job: held while a speaking span that it may be inside is open (and behind
// one held, so the live log stays in order), else judged at once.
func (r *hubRun) sentenceLocked(s kb.EchoSentence) {
	if len(r.held) > 0 || r.openFrom != nil && kb.EchoHeld(s, *r.openFrom) {
		r.held = append(r.held, s)
		return
	}
	r.judgeLocked(s)
}

// judgeLocked keeps or drops sentence s by the echo rule.
func (r *hubRun) judgeLocked(s kb.EchoSentence) {
	if kb.EchoOf(s, kb.EchoSpans(r.timeline)) == kb.EchoDrop {
		return
	}
	r.acceptLocked(s)
}

// acceptLocked takes sentence s into the hub's transcript and, while the role at its end is not off and the phone's
// text did not fill its time, into the live log, with its line frame. The hub's sentences coming again end a problem
// and the fallback.
func (r *hubRun) acceptLocked(s kb.EchoSentence) {
	text := strings.TrimSpace(s.Text)
	if text == "" {
		return
	}
	if s.End < s.Start {
		s.End = s.Start
	}
	r.hub = append(r.hub, kb.RecordingSegment{Start: s.Start, End: s.End, Text: text, Source: kb.SourceHub})
	r.hubDirty = true
	r.lastHubEnd = max(r.lastHubEnd, s.End)
	if r.upstream != "" || r.fatal {
		r.upstream, r.fatal = "", false
		r.tellProblemLocked()
	}
	if r.fallback {
		r.setFallbackLocked(false)
	}
	defer r.notifyLocked()
	if r.timeline.RoleAt(s.End, r.role) == kb.AttendanceOff {
		return // in the hub's transcript only
	}
	if kb.CoveredShare(s.Start, s.End, r.filled) > 0.5 {
		return // the phone's text said it already
	}
	n, err := r.e.LiveAppendHub(r.id, []kb.LiveSegment{{Start: s.Start, End: s.End, Text: text}})
	if err != nil {
		if !errors.Is(err, kb.ErrLiveFull) {
			r.s.logf("recording %s: live line: %v", r.id, err)
		}
		return
	}
	r.phoneSendLocked(map[string]any{"type": "line", "n": n, "start": s.Start, "end": s.End, "text": text})
}

// interimLocked is a caption of the live job: sent to the phone whatever the role, none while Persona speaks.
func (r *hubRun) interimLocked(start float64, text string) {
	if r.noInterim {
		r.noInterim = false // captions came
		r.tellProblemLocked()
	}
	if r.openFrom != nil {
		return
	}
	now := time.Now()
	r.interim = &liveInterim{Start: start, Text: text, At: now}
	r.interimRev = max(now.UnixMilli(), r.interimRev+1)
	r.phoneSendLocked(map[string]any{"type": "interim", "start": start, "text": text})
	if r.roleNowLocked() != kb.AttendanceOff {
		r.s.meetingLiveChanged(r.id, []string{kb.PartInterim})
	}
}

// clearInterimLocked: no caption stands (a span opened, the socket went).
func (r *hubRun) clearInterimLocked() {
	if r.interim == nil {
		return
	}
	r.interim = nil
	r.interimRev++
	r.s.meetingLiveChanged(r.id, []string{kb.PartInterim})
}

// putHubLocked writes the hub's live sentences to hub_segments.json (with transcript.md).
func (r *hubRun) putHubLocked() {
	if !r.hubDirty {
		return
	}
	r.hubPutAt = time.Now()
	if _, err := r.e.HubTranscriptPut(r.id, kb.HubPassLive, r.hub); err != nil {
		switch {
		case errors.Is(err, kb.ErrReadOnly):
			return // kept until it can be written
		case errors.Is(err, kb.ErrTranscriptionFinal), errors.Is(err, kb.ErrNotFound), errors.Is(err, kb.ErrNotHub):
		default:
			r.s.logf("recording %s: hub transcript: %v", r.id, err)
			return
		}
	}
	r.hubDirty = false
}

// ---- problems ----

// problemLocked is the problem that stands: the upstream's first, else no captions.
func (r *hubRun) problemLocked() string {
	switch {
	case r.upstream != "":
		return r.upstream
	case r.noInterim:
		return problemNoInterim
	}
	return ""
}

// setUpstreamLocked: the upstream's problem is p.
func (r *hubRun) setUpstreamLocked(p string) {
	if r.upstream != p {
		r.upstream, r.upstreamAt = p, time.Now()
	}
	r.tellProblemLocked()
}

// tellProblemLocked tells the phone and meeting.json the problem that stands, when it changed.
func (r *hubRun) tellProblemLocked() {
	p := r.problemLocked()
	if p == r.shown {
		return
	}
	r.shown = p
	var code any
	if p != "" {
		code = p
	}
	r.phoneSendLocked(map[string]any{"type": "problem", "code": code})
	if _, err := r.e.TranscriptionUpdate(r.id, func(t *kb.MeetingTranscription, _ kb.Recording) error {
		t.Problem = p
		return nil
	}); err != nil && !errors.Is(err, kb.ErrNotFound) && !errors.Is(err, kb.ErrRecordingDeleted) {
		r.s.logf("recording %s: transcription problem: %v", r.id, err)
	}
}

// ---- the end of a run ----

// stop ends the run: the phone's socket gets code (none: closed as at its end), the job is closed, the sentences held
// are judged and the hub's transcript is written (not after a delete or trim).
func (r *hubRun) stop(code string) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	gone := code == codeRecordingDeleted || code == codeTranscriptExpired
	if r.openFrom != nil && !gone {
		from := *r.openFrom
		r.expireSpanLocked(from, float64(r.edge)/kb.AudioRate)
	}
	if !gone {
		r.putHubLocked()
	}
	r.stopped, r.stopCode = true, code
	phone, job := r.phone, r.job
	r.phone, r.job = nil, nil
	r.notifyLocked()
	r.mu.Unlock()
	close(r.quit)
	if job != nil {
		job.close()
	}
	if phone != nil {
		if code == "" {
			phone.closeWith(1000)
		} else {
			phone.fail(code)
		}
	}
	r.end()
	r.s.hubTr.mu.Lock()
	if r.s.hubTr.by[r.id] == r {
		delete(r.s.hubTr.by, r.id)
	}
	r.s.hubTr.mu.Unlock()
}

// hubStop ends recording id's run, when it has one, telling its socket code.
func (s *Service) hubStop(id, code string) {
	if r := s.hubRunOf(id); r != nil {
		r.stop(code)
	}
}

// hubSweep ends the runs of recordings deleted, trimmed or past their live pass (each retention pass, each final
// round).
func (s *Service) hubSweep(e *kb.Engine) {
	s.hubTr.mu.Lock()
	runs := make([]*hubRun, 0, len(s.hubTr.by))
	for _, r := range s.hubTr.by {
		runs = append(runs, r)
	}
	s.hubTr.mu.Unlock()
	for _, r := range runs {
		_, rec, err := e.MeetingRead(r.id)
		switch {
		case errors.Is(err, kb.ErrRecordingDeleted), errors.Is(err, kb.ErrNotFound):
			r.stop(codeRecordingDeleted)
		case err != nil:
		case rec.Trimmed():
			r.stop(codeTranscriptExpired)
		case !rec.HubTranscribed():
			r.stop(codeNotHub)
		case !rec.Conv.Transcription.FinalFrom.IsZero():
			r.stop(codeTranscriptionFinal)
		}
	}
}

// ---- what the asks and the live view read ----

// hubInterim is recording id's latest caption for GET …/live: while the socket is up, the role at it is not off and
// it is at most interimShown old; nil otherwise.
func (s *Service) hubInterim(id string) *liveInterim {
	r := s.hubRunOf(id)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	in := r.interim
	if in == nil || r.phone == nil || r.ended || time.Since(in.At) > interimShown ||
		r.timeline.RoleAt(in.Start, r.role) == kb.AttendanceOff {
		return nil
	}
	c := *in
	return &c
}

// hubInterimRev is the revision of recording id's caption (its time in milliseconds), 0 without one.
func (s *Service) hubInterimRev(id string) int64 {
	r := s.hubRunOf(id)
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interimRev
}

// hubFlush asks the live job of recording id for its sentences up to recording second at; true once every sentence
// that ends before at is in the live log (none held back for a speaking span), false when ctx ended first or no job
// is transcribing it.
func (s *Service) hubFlush(ctx context.Context, id string, at float64) bool {
	r := s.hubRunOf(id)
	if r == nil {
		return false
	}
	for {
		r.mu.Lock()
		if r.flushedTo >= at && !slices.ContainsFunc(r.held, func(h kb.EchoSentence) bool { return h.End <= at }) {
			r.mu.Unlock()
			return true
		}
		j := r.job
		if j == nil || r.stopped {
			r.mu.Unlock()
			return false
		}
		if j.ready && !j.flushAsked && float64(j.sentS)/kb.AudioRate >= at {
			j.flushLocked()
		}
		wake := r.wake
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-wake:
		}
	}
}

// awaitHeard is recording id's live log for heard ask a: in device mode once it holds a.LiveNext positions
// (awaitLive); in hub mode, whatever live_next says, once the hub's sentences up to a.At are in (hubFlush), or in the
// fallback once an upload's segments reach it, waiting at most askLiveWait either way.
func (s *Service) awaitHeard(ctx context.Context, e *kb.Engine, id string, a kb.LiveAsk) (kb.Live, error) {
	l, rec, err := e.LiveRead(id)
	if err != nil {
		return l, err
	}
	if !rec.HubTranscribed() || a.At == nil {
		return awaitLive(ctx, e, id, a.LiveNext)
	}
	if r := s.hubRunOf(id); r != nil {
		wctx, cancel := context.WithTimeout(ctx, askLiveWait)
		if r.inFallback() {
			r.awaitDevice(wctx, *a.At)
		} else {
			s.hubFlush(wctx, id, *a.At)
		}
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return l, err
	}
	l, _, err = e.LiveRead(id)
	return l, err
}

// kickFinal has the final passes looked at now.
func (s *Service) kickFinal() {
	s.hubTr.mu.Lock()
	if s.hubTr.kick == nil {
		s.hubTr.kick = make(chan struct{}, 1)
	}
	k := s.hubTr.kick
	s.hubTr.mu.Unlock()
	select {
	case k <- struct{}{}:
	default:
	}
}

// transcriptionGlossary is what a job's start carries as its glossary: the meeting's title, its calendar title and
// attendees, then meeting_glossary; one line each, repeats (case-insensitive) and blanks dropped, terms over
// glossaryTermMost characters left out, at most glossaryMost. Data: a contribution uses it for spelling only.
func (s *Service) transcriptionGlossary(ctx context.Context, rec kb.Recording) []string {
	terms := []string{rec.Title}
	if c := rec.Calendar; c != nil {
		terms = append(terms, c.Title)
		terms = append(terms, c.Attendees...)
	}
	terms = append(terms, meetingGlossaryOf(s.settings(ctx))...)
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || len([]rune(t)) > glossaryTermMost ||
			slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, t) }) {
			continue
		}
		out = append(out, t)
		if len(out) == glossaryMost {
			break
		}
	}
	return out
}

// The glossary a job's start carries at most.
const (
	glossaryMost     = 256
	glossaryTermMost = 80
)

// transcriptionRef is the contribution that transcribes (meeting_transcription_extension: "" the built-in one).
func (s *Service) transcriptionRef(ctx context.Context) string {
	return meetingTranscriptionExtensionOf(s.settings(ctx))
}

// recordingLanguage is the recording's language tag: the job's hint, never a lock (every word is written in the
// language it was said in).
func recordingLanguage(rec kb.Recording) string { return strings.TrimSpace(rec.Language) }
