package voice

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// Volcengine's public Seeduplex realtime speech service. This is its documented public endpoint, not an ARK endpoint.
const (
	volcModel         = "1.2.6.1"
	volcDefaultURL    = "wss://openspeech.bytedance.com/api/v3/duplex/realtime/dialogue"
	volcDefaultVoice  = "zh_female_vv_jupiter_bigtts"
	volcRealtimeDoc   = "https://docs.volcengine.com/docs/doubao-voice/end-to-end-real-time-voice-full-duplex-version"
)

var volcVoices = []string{
	volcDefaultVoice,
	"zh_female_xiaohe_jupiter_bigtts",
	"zh_male_yunzhou_jupiter_bigtts",
	"zh_male_xiaotian_jupiter_bigtts",
}

// volcWire speaks Seeduplex's JSON Realtime events (volcRealtimeDoc). Its credentials, session lifecycle and function
// calls are not GPT-Live's. Persona's app protocol, turn tracking and approval cards stay independent of this wire.
type volcWire struct{}

func (volcWire) url(base, _ string) (string, error) { return base, nil }

// Seeduplex's new speech console issues an X-Api-Key. It must not also be sent as a GPT-Live Bearer credential.
func (volcWire) header() http.Header { return http.Header{"X-Api-Key": {""}} }

func (volcWire) start(s session) any {
	sess := map[string]any{
		"model":        s.Model,
		"instructions": s.Instructions,
		"audio": map[string]any{
			"input": map[string]any{"format": map[string]any{"type": "pcm", "rate": s.InputRate}},
			// The service's pcm is 32-bit; the app protocol carries PCM16, so explicitly request pcm_s16le.
			"output": map[string]any{"format": map[string]any{"type": "pcm_s16le", "rate": 24000}, "voice": s.Voice},
		},
	}
	if len(s.Tools) > 0 {
		sess["tools"] = s.Tools
	}
	return map[string]any{"type": "session.create", "session": sess}
}

func (volcWire) audio(pcm []byte) any {
	return map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(pcm)}
}

// A typed utterance or an announcement is user context for the next model turn. Seeduplex does not take Persona's
// delegation envelopes or commentary channels.
func (w volcWire) text(s string) any { return w.say(s, "")[0] }

func (volcWire) say(s, _ string) []any {
	var out []any
	for _, chunk := range chunkRunes(strings.TrimSpace(s), commentaryRunes) {
		out = append(out, map[string]any{"type": "conversation.item.create", "items": []any{
			map[string]any{"role": "user", "content": inputContent(chunk)},
		}})
	}
	return out
}

// A function result keeps the call_id as its reply target. Quiet progress is deliberately not sent: Persona's
// announcements use the same insertion gate as the other dialects, and only the final result should make a reply.
func (w volcWire) reply(callID, s string, final bool, _ string) []any {
	if !final {
		return nil
	}
	return []any{map[string]any{"type": "conversation.item.create", "items": []any{
		map[string]any{"call_id": callID, "role": "tool", "content": inputContent(clipRunes(s, commentaryRunes))},
	}}}
}

func (volcWire) acks() bool           { return false }
func (volcWire) close() any           { return map[string]any{"type": "session.close"} }
func (volcWire) interrupt() any       { return map[string]any{"type": "response.cancel"} }
func (volcWire) outputRate(int) int   { return 24000 }
func (volcWire) defaultVoice() string { return volcDefaultVoice }

func (volcWire) decode(raw []byte) []event {
	var f struct {
		Type       string `json:"type"`
		Delta      string `json:"delta"`
		Text       string `json:"text"`
		Transcript string `json:"transcript"`
		ResponseID string `json:"response_id"`
		Response   struct {
			ID string `json:"id"`
		} `json:"response"`
		Message    string          `json:"message"`
		Code       json.RawMessage `json:"code"`
		StatusCode json.RawMessage `json:"status_code"`
		Error      struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
		Items []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Args   string `json:"arguments"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	responseID := f.ResponseID
	if responseID == "" {
		responseID = f.Response.ID
	}
	switch f.Type {
	case "session.created", "session.updated":
		return []event{{kind: evStarted, rate: 24000}}
	case "response.output_audio.delta":
		pcm, err := base64.StdEncoding.DecodeString(f.Delta)
		if err != nil || len(pcm) == 0 {
			return nil
		}
		return []event{{kind: evAudio, audio: pcm}}
	case "conversation.item.input_audio_transcription.delta":
		return []event{{kind: evUser, text: f.Delta}}
	case "conversation.item.input_audio_transcription.completed":
		text := f.Transcript
		if text == "" {
			text = f.Text
		}
		return []event{{kind: evUser, text: text, replace: text != "", final: true}}
	case "conversation.item.input_audio_transcription.failed":
		return []event{{kind: evError, text: firstNonEmpty(f.Text, f.Message, "speech transcription failed")}}
	case "response.output_text.delta":
		return []event{{kind: evAssistant, text: f.Delta, name: "text"}}
	case "response.function_call_arguments.done":
		var out []event
		for _, item := range f.Items {
			if item.CallID == "" || item.Name == "" || item.Type != "" && item.Type != "function_call" {
				continue
			}
			// Seeduplex has no delegation id. Its call_id is the reply target; reply returns the matching tool item.
			out = append(out, event{kind: evToolCall, id: item.CallID, callID: item.CallID, name: item.Name, args: item.Args})
		}
		return out
	case "response.done":
		return []event{{kind: evTurnEnd, id: responseID}}
	case "response.canceled", "response.cancelled", "response.incomplete":
		return []event{{kind: evInterrupted, id: responseID}}
	case "error":
		text := firstNonEmpty(f.Error.Message, f.Message, f.Text)
		code := firstNonEmpty(jsonScalar(f.Error.Code), jsonScalar(f.Code), jsonScalar(f.StatusCode))
		return []event{{kind: evError, text: text, code: code}}
	}
	return nil
}

func jsonScalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// chunkRunes splits without cutting UTF-8, including when s is exactly at the limit.
func chunkRunes(s string, n int) []string {
	if n < 1 || s == "" {
		return []string{s}
	}
	var out []string
	for len([]rune(s)) > n {
		r := []rune(s)
		out = append(out, string(r[:n]))
		s = string(r[n:])
	}
	return append(out, s)
}
