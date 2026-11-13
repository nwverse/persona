package voice

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
)

// The transcription sessions' wire (docs/assistant/meeting-minutes.md "The built-in adapter"): the live dialect's
// session.start with text-only output (the service writes the sentences, it synthesizes no audio) and, for the
// caption session, the upstream's interim-caption extension. The call's wire (liveWire) is used as it is; the frames
// it does not read (the captions, a reply's whole text, usage, the session's close) are read here.

// trCaptionExtension is the upstream's interim-caption session extension and its version, negotiated in
// session.start's extensions: session.started echoes what was granted, and one missing from the echo was not.
const (
	trCaptionExtension        = "input_transcript_partial"
	trCaptionExtensionVersion = "v1"
)

// trCaptionSuffix ends the type of an interim-caption frame; the service puts its own prefix before it.
const trCaptionSuffix = ".input_transcript.partial"

// transcribeStart is a transcription session's session.start: the live dialect's, at 16 kHz, without a voice or
// tools and without any language setting (the language is never locked: mixed speech stays mixed), with text-only
// output unless textOnly is false (a service that refused it), and asking for interim captions when caption.
func transcribeStart(model, instructions string, caption, textOnly bool) map[string]any {
	start := liveWire{}.start(session{Model: model, InputRate: trRate, Instructions: instructions}).(map[string]any)
	sess := start["session"].(map[string]any)
	if textOnly {
		sess["output_modalities"] = []string{"text"}
	}
	if caption {
		sess["extensions"] = map[string]any{trCaptionExtension: trCaptionExtensionVersion}
	}
	return start
}

type tevKind int

const (
	tevStarted  tevKind = iota // the session is up; captions: the caption extension was granted
	tevCaption                 // an interim caption: item, at (session seconds), text; final: withdrawn when text is ""
	tevReply                   // a reply began
	tevText                    // a piece of the reply's text
	tevTextDone                // the reply's whole text (text, "" when the service does not repeat it)
	tevReplyEnd                // the reply is over (completed, failed, cut off)
	tevUsage                   // seconds: the session's billed audio seconds so far
	tevClosed                  // the service closed the session: reason, seconds when it said
	tevError                   // the service's error: code, param, text
)

// tevent is one thing a transcription session's service said.
type tevent struct {
	kind        tevKind
	text        string
	item        string
	code, param string
	reason      string
	at          float64 // tevCaption: when recognition of the sentence began, in seconds of the session's audio
	final       bool
	seconds     float64
	hasSeconds  bool
	captions    bool      // tevStarted
	expires     time.Time // tevStarted: when the service ends the session (session.started's expires_at; zero: not said)
	modalities  []string
}

// decodeTranscribe reads one frame of a transcription session's service. The call's decoder reads the replies'
// pieces, their start and end, and errors; the captions, a reply's whole text, usage and the session's close are read
// here. Anything else (the standard input caption among them: it is cut short while a model replies) is nothing.
func decodeTranscribe(raw []byte) []tevent {
	var f struct {
		Type    string  `json:"type"`
		ItemID  string  `json:"item_id"`
		StartMS float64 `json:"start_ms"`
		Text    string  `json:"text"`
		IsFinal bool    `json:"is_final"`
		Reason  string  `json:"reason"`
		Usage   *struct {
			Seconds *float64 `json:"seconds"`
		} `json:"usage"`
		Session struct {
			Extensions       map[string]any `json:"extensions"`
			OutputModalities []string       `json:"output_modalities"`
			ExpiresAt        *float64       `json:"expires_at"` // Unix seconds (or milliseconds)
		} `json:"session"`
		Event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	usage := func(e tevent) tevent {
		if f.Usage != nil && f.Usage.Seconds != nil {
			e.seconds, e.hasSeconds = *f.Usage.Seconds, true
		}
		return e
	}
	switch {
	case f.Type == "session.started":
		v, ok := f.Session.Extensions[trCaptionExtension]
		granted := ok && v != nil && v != false && v != ""
		e := tevent{kind: tevStarted, captions: granted, modalities: f.Session.OutputModalities}
		if x := f.Session.ExpiresAt; x != nil && *x > 0 && *x < 1e15 {
			at := *x
			if at >= 1e11 { // milliseconds (seconds that large are thousands of years away)
				at /= 1000
			}
			sec, frac := math.Modf(at)
			e.expires = time.Unix(int64(sec), int64(frac*1e9))
		}
		return []tevent{e}
	case strings.HasSuffix(f.Type, trCaptionSuffix):
		return []tevent{{kind: tevCaption, item: f.ItemID, at: f.StartMS / 1000, text: f.Text, final: f.IsFinal}}
	case f.Type == "session.usage.updated":
		if e := usage(tevent{kind: tevUsage}); e.hasSeconds {
			return []tevent{e}
		}
		return nil
	case f.Type == "session.closed":
		return []tevent{usage(tevent{kind: tevClosed, reason: f.Reason})}
	case f.Type == "response.event" && f.Event.Type == "response.output_text.done":
		return []tevent{{kind: tevTextDone, text: f.Event.Text}}
	}
	var out []tevent
	for _, e := range (liveWire{}).decode(raw) {
		switch e.kind {
		case evReply:
			out = append(out, tevent{kind: tevReply})
		case evAssistant:
			if e.name == "text" {
				out = append(out, tevent{kind: tevText, text: e.text})
			}
		case evTurnEnd, evInterrupted:
			out = append(out, tevent{kind: tevReplyEnd})
		case evError:
			out = append(out, tevent{kind: tevError, code: e.code, param: e.param, text: e.text})
		}
	}
	return out
}

// errNoCaptions: the service refused the caption extension as the session started.
var errNoCaptions = errors.New("the service refused the interim-caption extension")

// refusedField says which field of a transcription session's start the service refused: "captions" (the extension),
// "text_only" (output_modalities), or "" (not one of those: a refused key, a service that is down, anything else).
// An error about a field it does not know that names neither is the caption extension's on a caption session (the
// one thing it has beyond a verbatim session's start), else the text-only output's.
func refusedField(r *sessionRefused, caption bool) string {
	words := strings.ToLower(r.param + " " + r.text)
	switch {
	case strings.Contains(words, "extension") || strings.Contains(words, trCaptionExtension) || strings.Contains(words, "partial"):
		if caption {
			return "captions"
		}
	case strings.Contains(words, "modalit") || strings.Contains(words, "output"):
		return "text_only"
	}
	if r.code == "unsupported_extension" && caption {
		return "captions"
	}
	if slices.Contains(fieldCodes, r.code) && r.param == "" {
		if caption {
			return "captions"
		}
		return "text_only"
	}
	return ""
}
