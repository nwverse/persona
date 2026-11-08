package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestVolcDialectConversation(t *testing.T) {
	hub := newFakeHub(t)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.create":
			u.send(c, map[string]any{"type": "session.created", "session": map[string]any{"id": "s1"}})
		case "conversation.item.create":
			items, _ := f["items"].([]any)
			if len(items) == 0 || items[0].(map[string]any)["role"] != "tool" {
				u.send(c, map[string]any{"type": "conversation.item.input_audio_transcription.delta", "delta": "查咖啡"})
				u.send(c, map[string]any{"type": "conversation.item.input_audio_transcription.completed", "transcript": "查咖啡"})
				u.send(c, map[string]any{"type": "response.output_text.delta", "response_id": "r1", "delta": "我来查。"})
				u.send(c, map[string]any{"type": "response.output_audio.started", "response_id": "r1"})
				u.send(c, map[string]any{"type": "response.output_audio.delta", "response_id": "r1", "delta": b64([]byte{1, 0, 2, 0})})
				u.send(c, map[string]any{"type": "response.function_call_arguments.done", "items": []map[string]any{
					{"type": "function_call", "call_id": "call-1", "name": "search_knowledge", "arguments": `{"query":"咖啡"}`},
				}})
				u.send(c, map[string]any{"type": "response.done", "response_id": "r1"})
			}
		}
	})
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "volc", URL: up.url(), Dialect: DialectVolc, Key: "test-key"}}}, hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	ready := a.until(ofType("ready"))
	if ready["model"] != "volc/1.2.6.1" || ready["input_rate"] != float64(16000) || ready["output_rate"] != float64(24000) || ready["voice"] != volcDefaultVoice {
		t.Fatalf("ready: %v", ready)
	}
	start := up.next("session.create")["session"].(map[string]any)
	audio := start["audio"].(map[string]any)
	input := audio["input"].(map[string]any)["format"].(map[string]any)
	output := audio["output"].(map[string]any)
	if start["model"] != volcModel || input["type"] != "pcm" || input["rate"] != float64(16000) ||
		output["format"].(map[string]any)["type"] != "pcm_s16le" || output["format"].(map[string]any)["rate"] != float64(24000) || output["voice"] != volcDefaultVoice {
		t.Fatalf("session.create: %v", start)
	}
	tools, _ := start["tools"].([]any)
	if len(tools) != len(toolNames) || tools[0].(map[string]any)["name"] != "search_knowledge" {
		t.Fatalf("Seeduplex function declarations: %v", tools)
	}
	if up.headers[0].Get("X-Api-Key") != "test-key" || up.headers[0].Get("Authorization") != "" {
		t.Fatalf("credentials: X-Api-Key is set, Authorization is %q", up.headers[0].Get("Authorization"))
	}
	a.send(map[string]any{"type": "text", "text": "查咖啡"})
	up.nextMatch("a user context item", func(f map[string]any) bool {
		items, _ := f["items"].([]any)
		return f["type"] == "conversation.item.create" && len(items) == 1 && items[0].(map[string]any)["role"] == "user"
	})
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "call-1" && f["done"] == false })
	a.until(finalLine("assistant", "我来查。"))
	select {
	case pcm := <-a.audio:
		if string(pcm) != string([]byte{1, 0, 2, 0}) {
			t.Fatalf("PCM16 output: %v", pcm)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no audio")
	}

	result := up.nextMatch("the tool result for the same call_id", func(f map[string]any) bool {
		items, _ := f["items"].([]any)
		return f["type"] == "conversation.item.create" && len(items) == 1 && items[0].(map[string]any)["role"] == "tool"
	})
	item := result["items"].([]any)[0].(map[string]any)
	text := item["content"].([]any)[0].(map[string]any)["text"].(string)
	if item["call_id"] != "call-1" || !strings.Contains(text, "x likes 咖啡") {
		t.Fatalf("function result: %v", result)
	}
	a.until(func(f map[string]any) bool { return f["type"] == "tool" && f["id"] == "call-1" && f["done"] == true })

	_ = a.c.Write(context.Background(), websocket.MessageBinary, []byte{9, 0})
	deadline := time.Now().Add(5 * time.Second)
	for up.count(func(f map[string]any) bool {
		return f["type"] == "input_audio_buffer.append" && f["audio"] == b64([]byte{9, 0})
	}) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("microphone PCM did not reach input_audio_buffer.append")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.send(map[string]any{"type": "interrupt"})
	up.next("response.cancel")
	a.protocolOne()
}

func TestVolcDialectDelegatedTask(t *testing.T) {
	hub := newFakeHub(t)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.create":
			u.send(c, map[string]any{"type": "session.created", "session": map[string]any{"id": "s1"}})
		case "conversation.item.create":
			items, _ := f["items"].([]any)
			if len(items) == 0 || items[0].(map[string]any)["role"] == "tool" {
				return
			}
			u.send(c, map[string]any{"type": "conversation.item.input_audio_transcription.completed", "transcript": "查明天的日程"})
			u.send(c, map[string]any{"type": "response.function_call_arguments.done", "items": []map[string]any{
				{"type": "function_call", "call_id": "task-1", "name": "ask_persona", "arguments": `{"task":"查明天的日程"}`},
			}})
		}
	})
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "volc", URL: up.url(), Dialect: DialectVolc, Key: "test-key"}}}, hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	a.until(ofType("ready"))
	a.send(map[string]any{"type": "text", "text": "查明天的日程"})
	result := up.nextMatch("the main conversation answer on its tool call", func(f map[string]any) bool {
		items, _ := f["items"].([]any)
		return f["type"] == "conversation.item.create" && len(items) == 1 && items[0].(map[string]any)["role"] == "tool"
	})
	item := result["items"].([]any)[0].(map[string]any)
	if item["call_id"] != "task-1" || !strings.Contains(item["content"].([]any)[0].(map[string]any)["text"].(string), "明天下午三点有会") {
		t.Fatalf("delegated answer: %v", result)
	}
	msgs := hub.messages()
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Text, "查明天的日程\n") {
		t.Fatalf("main conversation got %+v", msgs)
	}
}

func TestVolcEnergyBargeInCancelsUpstream(t *testing.T) {
	hub := agentHub(t)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] == "session.create" {
			u.send(c, map[string]any{"type": "session.created", "session": map[string]any{"id": "s1"}})
			u.send(c, map[string]any{"type": "response.output_audio.started", "response_id": "r1"})
		}
	})
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "volc", URL: up.url(), Dialect: DialectVolc, Key: "test-key"}}}, hub.server(t))
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	if events := (volcWire{}).decode([]byte(`{"type":"response.output_audio.started","response_id":"r1"}`)); len(events) != 0 {
		t.Fatalf("audio start is not a complete reply-boundary protocol: %v", events)
	}
	// This large PCM16 block leaves audio playing while the user speaks for more than bargeMin. Establish the noise
	// floor with silence first, as the phone does before a user talks.
	_ = a.c.Write(context.Background(), websocket.MessageBinary, make([]byte, 640))
	up.conns[0].Write(context.Background(), websocket.MessageText, []byte(`{"type":"response.output_audio.delta","response_id":"r1","delta":"`+b64(make([]byte, 24000*2))+`"}`))
	for i := 0; i < 40; i++ {
		_ = a.c.Write(context.Background(), websocket.MessageBinary, tone(320, 3000))
	}
	up.next("response.cancel")
}

func TestVolcConnectionErrorIsLocalized(t *testing.T) {
	hub := newFakeHub(t)
	for _, tc := range []struct {
		lang i18n.Lang
		text string
	}{
		{i18n.ZhHans, "同项目的语音服务 API Key"},
		{i18n.En, "same project"},
		{i18n.Ja, "同じプロジェクト"},
	} {
		t.Run(string(tc.lang), func(t *testing.T) {
			pl := testPlugin(t, Config{Providers: []Provider{{
				ID: "volc", URL: "ws://127.0.0.1:1", Dialect: DialectVolc, Key: "test-key",
			}}}, hub.server(t))
			a := dialApp(t, pl, map[string]any{"type": "start"}, http.Header{"X-Persona-Lang": {string(tc.lang)}})
			f := a.until(ofType("error"))
			if f["fatal"] != true || !strings.Contains(f["message"].(string), tc.text) || !strings.Contains(f["message"].(string), "connection refused") {
				t.Fatalf("localized connection diagnosis: %v", f)
			}
		})
	}
}

func TestVolcWireDecodesErrorsAndEmptyToolItems(t *testing.T) {
	w := volcWire{}
	if events := w.decode([]byte(`{"type":"error","status_code":401,"message":"invalid key"}`)); len(events) != 1 || events[0].kind != evError || events[0].code != "401" || events[0].text != "invalid key" {
		t.Fatalf("error: %+v", events)
	}
	if events := w.decode([]byte(`{"type":"response.function_call_arguments.done","items":[{"call_id":"","name":"bad"},{"type":"message","call_id":"c","name":"bad"}]}`)); len(events) != 0 {
		t.Fatalf("invalid calls: %+v", events)
	}
	if events := w.decode([]byte(`{"type":"response.output_audio.delta","delta":"not base64"}`)); len(events) != 0 {
		t.Fatalf("invalid audio: %+v", events)
	}
	quiet := w.reply("c", "progress", false, "")
	if len(quiet) != 0 {
		t.Fatalf("quiet progress must not become a tool result: %v", quiet)
	}
}

func TestVolcModelAndVoiceCatalog(t *testing.T) {
	hub := newFakeHub(t)
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "volc", URL: volcDefaultURL, Dialect: DialectVolc, Key: "test-key"}}}, hub.server(t))
	cat := pl.cat
	p, model, err := cat.resolve(t.Context(), "")
	if err != nil || p.ID != "volc" || model != volcModel {
		t.Fatalf("default model: %v %s %v", p, model, err)
	}
	voices, err := cat.voices(t.Context(), "volc/"+volcModel)
	if err != nil || voices.Default != volcDefaultVoice || len(voices.Voices) != len(volcVoices) || voices.Voices[0].ID != volcDefaultVoice {
		t.Fatalf("voices: %+v %v", voices, err)
	}
}

// This opt-in test makes a real, potentially billable speech session. Supply the speech service's API key through the
// environment, never in source. It verifies configuration, handshake and PCM16 output; microphone, captions, tools and
// interruption need a separate real-service microphone / account acceptance run.
func TestVolcRealtimeService(t *testing.T) {
	key := os.Getenv("PERSONA_VOLCENGINE_API_KEY")
	if os.Getenv("PERSONA_VOLCENGINE_E2E") != "1" || key == "" {
		t.Skip("real billable service: PERSONA_VOLCENGINE_E2E=1 and PERSONA_VOLCENGINE_API_KEY")
	}
	hub := newFakeHub(t)
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "volc", URL: volcDefaultURL, Dialect: DialectVolc, Key: key}}}, hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start"})
	ready := a.until(ofType("ready"))
	if ready["model"] != "volc/"+volcModel || ready["input_rate"] != float64(16000) || ready["output_rate"] != float64(24000) {
		t.Fatalf("ready: %v", ready)
	}
	a.send(map[string]any{"type": "text", "text": "请只回答：语音连接测试成功。"})
	a.until(finalLine("user", "请只回答：语音连接测试成功。"))
	deadline := time.After(60 * time.Second)
	for {
		select {
		case <-a.audio:
			return
		case f := <-a.frames:
			if f["type"] == "error" {
				b, _ := json.Marshal(f)
				t.Fatalf("real service refused the session: %s", b)
			}
		case <-deadline:
			t.Fatal("real service returned no PCM16 audio within 60 seconds")
		}
	}
}
