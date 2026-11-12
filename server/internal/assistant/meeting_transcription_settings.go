package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

var transcriptionReadyInterval = 30 * time.Second

const transcriptionInfoTimeout = 3 * time.Second

func meetingTranscriptionDefaultOf(v Settings) string {
	if v.MeetingTranscriptionDefault != nil && *v.MeetingTranscriptionDefault == "hub" {
		return "hub"
	}
	return "device"
}

func meetingTranscriptionExtensionOf(v Settings) string {
	if v.MeetingTranscriptionExtension != nil {
		return *v.MeetingTranscriptionExtension
	}
	return ""
}

func meetingGlossaryOf(v Settings) []string {
	if v.MeetingGlossary != nil {
		if terms, err := cleanGlossary(*v.MeetingGlossary); err == nil {
			return terms
		}
	}
	return []string{}
}

// cleanGlossary keeps spellings, not instructions: one line per term, with the first spelling of a repeat kept.
func cleanGlossary(terms []string) ([]string, error) {
	out := make([]string, 0, min(len(terms), 200))
	for _, term := range terms {
		term = strings.Join(strings.Fields(term), " ")
		if utf8.RuneCountInString(term) > 40 {
			return nil, i18n.Err("assistant-meeting.bad_glossary")
		}
		if term == "" || slices.ContainsFunc(out, func(old string) bool { return strings.EqualFold(old, term) }) {
			continue
		}
		out = append(out, term)
		if len(out) > 200 {
			return nil, i18n.Err("assistant-meeting.bad_glossary")
		}
	}
	return out, nil
}

// readMeetingTranscriptionSettings decodes the raw fields so even wrong JSON types have their documented codes.
// A missing field leaves it unchanged; null is not a string or a list of strings.
func (s *Service) readMeetingTranscriptionSettings(v *Settings, mode, glossary, extension json.RawMessage) string {
	if mode != nil {
		if err := json.Unmarshal(mode, &v.MeetingTranscriptionDefault); err != nil || v.MeetingTranscriptionDefault == nil ||
			(*v.MeetingTranscriptionDefault != "device" && *v.MeetingTranscriptionDefault != "hub") {
			return "bad_transcription_mode"
		}
	}
	if glossary != nil {
		var terms []*string
		if err := json.Unmarshal(glossary, &terms); err != nil || terms == nil {
			return "bad_glossary"
		}
		values := make([]string, 0, len(terms))
		for _, term := range terms {
			if term == nil {
				return "bad_glossary"
			}
			values = append(values, *term)
		}
		clean, err := cleanGlossary(values)
		if err != nil {
			return "bad_glossary"
		}
		v.MeetingGlossary = &clean
	}
	if extension != nil {
		if err := json.Unmarshal(extension, &v.MeetingTranscriptionExtension); err != nil || v.MeetingTranscriptionExtension == nil {
			return "bad_transcription_extension"
		}
		if ref := *v.MeetingTranscriptionExtension; ref != "" {
			parts := strings.Split(ref, "/")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.TrimSpace(ref) != ref ||
				s.trans == nil || !s.trans.IsTranscriptionContribution(ref) {
				return "bad_transcription_extension"
			}
		}
	}
	return ""
}

// transcriptionReadiness caches only availability, never the user's choice. A changed setting or registry report
// invalidates it before another probe can publish an old result. Slow probes do not hold up readers of the cache.
type transcriptionReadiness struct {
	ready atomic.Bool
	probe sync.Mutex
	mu    sync.Mutex
	at    time.Time
	gen   uint64
	wake  chan struct{}
}

func (s *Service) transcriptionReadinessChanged() {
	c := &s.transcriptionReady
	c.mu.Lock()
	c.gen++
	c.at = time.Time{}
	c.ready.Store(false)
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
	c.mu.Unlock()
}

func (s *Service) transcriptionReadyNow() bool {
	s.refreshTranscriptionReady(s.runCtx())
	return s.transcriptionReady.ready.Load()
}

func (s *Service) refreshTranscriptionReady(ctx context.Context) {
	c := &s.transcriptionReady
	if !c.probe.TryLock() {
		return
	}
	defer c.probe.Unlock()
	for ctx.Err() == nil {
		c.mu.Lock()
		at, gen := c.at, c.gen
		c.mu.Unlock()
		if !at.IsZero() && time.Since(at) < transcriptionReadyInterval {
			return
		}
		ready := false
		if s.trans != nil {
			probeCtx, cancel := context.WithTimeout(ctx, transcriptionInfoTimeout)
			v, ok := s.settingsLastGood(probeCtx)
			if ok {
				info, err := s.trans.TranscriptionInfo(probeCtx, meetingTranscriptionExtensionOf(v))
				// meeting-minutes.md "Which one transcribes": protocol 1 with live and no setup.
				ready = err == nil && info.Protocol == extwire.TranscriptionProtocol && info.Setup == "" &&
					slices.Contains(info.Features, extwire.TranscriptionLive)
			}
			cancel()
		}
		c.mu.Lock()
		if c.gen != gen {
			c.mu.Unlock()
			continue // settings changed during the probe: never publish its old answer
		}
		c.ready.Store(ready)
		c.at = time.Now()
		c.mu.Unlock()
		return
	}
}

func (s *Service) transcriptionReadyLoop(ctx context.Context) {
	c := &s.transcriptionReady
	c.mu.Lock()
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
	wake := c.wake
	c.mu.Unlock()
	ticker := time.NewTicker(transcriptionReadyInterval)
	defer ticker.Stop()
	s.refreshTranscriptionReady(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Probe on each tick even if the previous probe finished just after its tick.
			c.mu.Lock()
			c.at = time.Time{}
			c.mu.Unlock()
		case <-wake:
		}
		s.refreshTranscriptionReady(ctx)
	}
}

func (s *Service) getMeetingTranscriptionDefault(ctx context.Context) (string, error) {
	out, err := json.Marshal(map[string]any{
		"meeting_transcription_default": meetingTranscriptionDefaultOf(s.settings(ctx)),
		"meeting_transcription_ready":   s.transcriptionReadyNow(),
	})
	return string(out), err
}

func (s *Service) setMeetingTranscriptionDefault(ctx context.Context, raw json.RawMessage) (*ownChange, error) {
	var mode string
	if err := json.Unmarshal(raw, &mode); err != nil || (mode != "device" && mode != "hub") {
		return nil, i18n.Err("assistant-meeting.bad_transcription_mode")
	}
	if s.store() == nil {
		return nil, i18n.Err("assistant-settings_tool.meeting_transcription_no_database")
	}
	before := meetingTranscriptionDefaultOf(s.settings(ctx))
	if mode == before {
		return &ownChange{summary: i18n.T(s.lang(ctx), "assistant-settings_tool.meeting_transcription_unchanged")}, nil
	}
	ready := s.transcriptionReadyNow()
	return &ownChange{cardText: func(l i18n.Lang) string {
		key := "assistant-settings_tool.change_meeting_transcription_default"
		if !ready {
			key = "assistant-settings_tool.meeting_transcription_not_ready"
		}
		return i18n.T(l, key, i18n.Args{
			"before": i18n.T(l, "assistant-settings_tool.meeting_transcription_"+before),
			"after":  i18n.T(l, "assistant-settings_tool.meeting_transcription_"+mode),
		})
	}, apply: func(ctx context.Context) (string, error) {
		// The database may have gone away while the approval card was open.
		if s.store() == nil {
			return "", i18n.Err("assistant-settings_tool.meeting_transcription_no_database")
		}
		if _, err := s.updateSettings(ctx, func(v *Settings) error { v.MeetingTranscriptionDefault = &mode; return nil }); err != nil {
			return "", err
		}
		s.settingsChanged(ctx)
		return i18n.T(s.lang(ctx), "assistant-settings_tool.meeting_transcription_applied", i18n.Args{
			"mode": i18n.T(s.lang(ctx), "assistant-settings_tool.meeting_transcription_"+mode),
		}), nil
	}}, nil
}
