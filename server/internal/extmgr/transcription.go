package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/voice"
)

// Meeting transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "The `transcription` extension point"): the
// manager's access to the transcription contributions, for the assistant in this process. Only the manager dials
// them, over the extension's own socket; the apps' forward refuses every path under /transcription/
// (transcriptionPath).

// transcriptionPoint is the extension point's name (extension.Points).
const transcriptionPoint = "transcription"

// transcriptionInfoTimeout bounds one GET /transcription/{cid}/info (a variable: tests shorten it).
var transcriptionInfoTimeout = 3 * time.Second

// Limits of what a contribution answers: its /info, and one text frame of a job (a sentence, a warning).
const (
	transcriptionInfoMost = 64 << 10
	transcriptionReadMost = 1 << 20
)

// ErrNoTranscriber: the transcription contribution ref names is not installed, not enabled or not running.
var ErrNoTranscriber = errors.New("no transcription contribution is running")

// TranscriptionInfo is ref's GET /transcription/{cid}/info. ref is the assistant's meeting_transcription_extension:
// "" the built-in voice extension's contribution, else "<extension id>/<contribution id>". An extension started by
// requests is started for it, as for any relayed request; reading it never opens a job.
func (h *Manager) TranscriptionInfo(ctx context.Context, ref string) (extwire.TranscriptionInfo, error) {
	var info extwire.TranscriptionInfo
	hp, cid, end, err := h.beginTranscriber(ctx, ref)
	if err != nil {
		return info, err
	}
	defer end()
	ctx, cancel := context.WithTimeout(ctx, transcriptionInfoTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://plugin/transcription/"+cid+"/info", nil)
	if err != nil {
		return info, err
	}
	res, err := hp.client.Do(req)
	if err != nil {
		return info, fmt.Errorf("transcription %s/%s: %w", hp.id, cid, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, transcriptionInfoMost))
		return info, fmt.Errorf("transcription %s/%s: info answered %s", hp.id, cid, res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, transcriptionInfoMost)).Decode(&info); err != nil {
		return info, fmt.Errorf("transcription %s/%s: info: %w", hp.id, cid, err)
	}
	return info, nil
}

// TranscriptionDial opens one job socket of ref's (GET /transcription/{cid}/ws); done ends the extension's open
// request, once the job is over (calling it again does nothing). The socket's context is the caller's to keep: it is
// not tied to ctx past the handshake. Its text frames may be up to 1 MiB.
func (h *Manager) TranscriptionDial(ctx context.Context, ref string) (conn *websocket.Conn, done func(), err error) {
	hp, cid, end, err := h.beginTranscriber(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	conn, res, err := websocket.Dial(ctx, "ws://plugin/transcription/"+cid+"/ws", &websocket.DialOptions{HTTPClient: hp.client})
	if err != nil {
		end()
		if res != nil {
			return nil, nil, fmt.Errorf("transcription %s/%s: the job was refused with %s", hp.id, cid, res.Status)
		}
		return nil, nil, fmt.Errorf("transcription %s/%s: %w", hp.id, cid, err)
	}
	conn.SetReadLimit(transcriptionReadMost)
	var once sync.Once
	return conn, func() { once.Do(end) }, nil
}

// IsTranscriptionContribution says whether ref names an installed extension's transcription contribution (whether
// or not it is enabled, or switched off). "" is the built-in voice extension's, there while that extension is.
func (h *Manager) IsTranscriptionContribution(ref string) bool {
	ps := h.extensions
	if ps == nil {
		return false
	}
	id, cid, ok := ps.transcriptionRef(ref)
	if !ok {
		return false
	}
	p := ps.reg.Snapshot(id)
	if p == nil || p.Full == nil || (ref == "" && !ps.isVoice(p)) {
		return false
	}
	return slices.Contains(p.Full.Items(transcriptionPoint), cid)
}

// transcriptionRef is the extension and contribution ref names: "" the built-in voice extension's
// (voice.TranscriptionContribution), else "<extension id>/<contribution id>".
func (ps *extensions) transcriptionRef(ref string) (id, cid string, ok bool) {
	if ref == "" {
		return ps.voiceID, voice.TranscriptionContribution, ps.voiceID != ""
	}
	id, cid, ok = strings.Cut(ref, "/")
	return id, cid, ok && extension.ValidID(id) && extension.ValidID(cid)
}

// beginTranscriber is the running extension of ref's contribution, with the request to it begun
// (host.Proc.BeginHTTP): end ends it. Installing an extension never makes it the transcriber: ref names it, or it
// is the built-in voice extension. ErrNoTranscriber when the contribution is not there, the extension or the
// contribution is switched off, or the program does not run.
func (h *Manager) beginTranscriber(ctx context.Context, ref string) (hp *hubExtension, cid string, end func(), err error) {
	ps := h.extensions
	if ps == nil {
		return nil, "", nil, ErrNoTranscriber
	}
	id, cid, ok := ps.transcriptionRef(ref)
	if !ok {
		return nil, "", nil, ErrNoTranscriber
	}
	p := ps.reg.Snapshot(id)
	switch {
	case p == nil || p.Manifest == nil || (ref == "" && !ps.isVoice(p)):
		return nil, "", nil, ErrNoTranscriber
	case !p.Record.Enabled || p.Problem != "" || !slices.Contains(p.Manifest.Items(transcriptionPoint), cid):
		return nil, "", nil, fmt.Errorf("transcription %s/%s is switched off: %w", p.Record.ID, cid, ErrNoTranscriber)
	}
	hp = ps.get(p.Record.ID)
	if hp == nil || hp.proc == nil || hp.client == nil {
		return nil, "", nil, fmt.Errorf("transcription %s/%s: %w", p.Record.ID, cid, ErrNoTranscriber)
	}
	end, err = hp.proc.BeginHTTP(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("transcription %s/%s: %w (%v)", hp.id, cid, ErrNoTranscriber, err)
	}
	if !hp.proc.Ready() {
		end()
		return nil, "", nil, fmt.Errorf("transcription %s/%s: %w", hp.id, cid, ErrNoTranscriber)
	}
	return hp, cid, end, nil
}

// transcriptionPath says whether rest, a path on an extension's own HTTP side (the part after
// /api/v1/extensions/<id>), is under /transcription/: the manager's only, never the apps' forward. rest is the
// decoded path, so an escaped spelling (%74ranscription, %2F) is caught too, and dot segments and doubled slashes
// are cleaned first; the case is ignored, for a program whose router ignores it.
func transcriptionPath(rest string) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(path.Clean("/"+rest), "/"), "/")
	return strings.EqualFold(first, transcriptionPoint)
}
