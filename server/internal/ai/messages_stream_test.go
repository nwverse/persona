package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// messagesStream sends one request to a Messages API that answers with body (an event stream unless it is one JSON
// message), and returns the reply, whether the upstream answered, and the request it got.
func messagesStream(t *testing.T, body string) (Reply, bool, error, map[string]any, http.Header) {
	t.Helper()
	var got map[string]any
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		header = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&got)
		if !strings.HasPrefix(strings.TrimSpace(body), "{") {
			w.Header().Set("content-type", "text/event-stream")
		}
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	reply, answered, err := c.messages(context.Background(), c.http, "k", Request{System: "s",
		Messages: []Message{{RoleUser, "a"}}, MaxTokens: 64})
	return reply, answered, err, got, header
}

// sse is the event stream of events, each "<event name> <data>", written the way the Messages API does.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		name, data, _ := strings.Cut(e, " ")
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, data)
	}
	return b.String()
}

const (
	evStart = `message_start {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"model-x","content":[],"stop_reason":null,"usage":{"input_tokens":120,"cache_read_input_tokens":30,"cache_creation_input_tokens":5,"output_tokens":1}}}`
	evPing  = `ping {"type":"ping"}`
	evStop  = `message_stop {"type":"message_stop"}`
)

func evBlock(i int, typ string) string {
	return fmt.Sprintf(`content_block_start {"type":"content_block_start","index":%d,"content_block":{"type":%q,"text":""}}`, i, typ)
}

func evText(i int, text string) string {
	b, _ := json.Marshal(text)
	return fmt.Sprintf(`content_block_delta {"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, i, b)
}

func evDelta(stop string, out int) string {
	return fmt.Sprintf(`message_delta {"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":%d}}`, stop, out)
}

// The request streams, and the reply is the text blocks' text with the model, the stop reason and the usage totals.
func TestMessagesStreamReply(t *testing.T) {
	reply, answered, err, req, header := messagesStream(t, sse(evStart, evBlock(0, "text"), evPing,
		evText(0, "{\"ok\":"), evText(0, "true}"), `content_block_stop {"type":"content_block_stop","index":0}`,
		evDelta("end_turn", 42), evStop))
	if err != nil || !answered {
		t.Fatalf("err %v, answered %v", err, answered)
	}
	want := Reply{Text: `{"ok":true}`, Model: "model-x",
		Usage: Usage{InputTokens: 120, OutputTokens: 42, CacheReadInputTokens: 30, CacheCreationInputTokens: 5}}
	if reply != want {
		t.Fatalf("reply %+v, want %+v", reply, want)
	}
	if req["stream"] != true || req["model"] != "m" || req["max_tokens"] != float64(64) || req["system"] != "s" {
		t.Fatalf("request %v", req)
	}
	if header.Get("accept") != "text/event-stream" || header.Get("anthropic-version") == "" {
		t.Fatalf("headers %v", header)
	}
}

// Text comes from every text block in order; a thinking block's deltas and a tool's input are not part of it.
func TestMessagesStreamKeepsOnlyText(t *testing.T) {
	reply, _, err, _, _ := messagesStream(t, sse(evStart,
		`content_block_start {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`content_block_delta {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
		`content_block_delta {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"x"}}`,
		evBlock(1, "text"), evText(1, "first "),
		`content_block_start {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t","name":"n","input":{}}}`,
		`content_block_delta {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`,
		evBlock(3, "text"), evText(3, "second"), evDelta("end_turn", 9), evStop))
	if err != nil || reply.Text != "first second" {
		t.Fatalf("reply %+v, err %v", reply, err)
	}
}

// A reply that stopped at max_tokens is Truncated, so JSONMax asks again with more room; a refusal is a RefusalError.
func TestMessagesStreamStopReasons(t *testing.T) {
	reply, _, err, _, _ := messagesStream(t, sse(evStart, evBlock(0, "text"), evText(0, `{"half":`), evDelta("max_tokens", 64), evStop))
	if err != nil || !reply.Truncated || reply.Text != `{"half":` {
		t.Fatalf("max_tokens: reply %+v, err %v", reply, err)
	}
	reply, answered, err, _, _ := messagesStream(t, sse(evStart, evDelta("refusal", 3), evStop))
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.StopReason != "refusal" || !answered || reply.Text != "" || reply.Usage.OutputTokens != 3 {
		t.Fatalf("refusal: reply %+v, answered %v, err %v", reply, answered, err)
	}
}

// A stream cut before message_stop (and before any stop reason) is only part of the answer: an error wrapping
// io.ErrUnexpectedEOF, which Transient counts as a failed connection. A stop reason with message_stop missing is
// a finished message.
func TestMessagesStreamCutFails(t *testing.T) {
	for name, body := range map[string]string{
		"after text":    sse(evStart, evBlock(0, "text"), evText(0, "half a ti")),
		"before any":    sse(evStart),
		"empty":         "",
		"only comments": ": keep-alive\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			reply, answered, err, _, _ := messagesStream(t, body)
			if !errors.Is(err, io.ErrUnexpectedEOF) || !Transient(err) || reply.Text != "" || !answered {
				t.Fatalf("reply %+v, answered %v, err %v; want no text and io.ErrUnexpectedEOF", reply, answered, err)
			}
		})
	}
	reply, _, err, _, _ := messagesStream(t, sse(evStart, evBlock(0, "text"), evText(0, "whole"), evDelta("end_turn", 2)))
	if err != nil || reply.Text != "whole" {
		t.Fatalf("no message_stop after the stop reason: reply %+v, err %v", reply, err)
	}
}

// Events are framed the SSE way (SSEEvents): data over several lines, CRLF, no event field, no blank line at the end.
func TestMessagesStreamFraming(t *testing.T) {
	body := "data: {\"type\":\"message_start\",\r\ndata: \"message\":{\"model\":\"model-x\"}}\r\n\r\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\r\n\r\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\r\n\r\n" +
		"data: {\"type\":\"message_stop\"}"
	reply, _, err, _, _ := messagesStream(t, body)
	if err != nil || reply.Text != "hi" || reply.Model != "model-x" {
		t.Fatalf("reply %+v, err %v", reply, err)
	}
}

// A server that ignores "stream" and answers one JSON message is read as before.
func TestMessagesWholeJSONAnswer(t *testing.T) {
	reply, answered, err, _, _ := messagesStream(t, "\n "+`{"model":"model-x","content":[{"type":"text","text":"hello"}],"stop_reason":"max_tokens","usage":{"input_tokens":3,"output_tokens":4}}`)
	if err != nil || !answered || reply != (Reply{Text: "hello", Truncated: true, Model: "model-x", Usage: Usage{InputTokens: 3, OutputTokens: 4}}) {
		t.Fatalf("reply %+v, answered %v, err %v", reply, answered, err)
	}
}

// An error event after the 200 is the failure it reports: overloaded_error and rate_limit_error hold the connection
// like HTTP 529 and 429 (a rate limit that speaks of quota as quota), and nothing more is sent while it is held; any
// other error is an ordinary error and the next call goes out.
func TestMessagesStreamErrorEvents(t *testing.T) {
	for _, c := range []struct {
		name, event, reason, detail string
		pause                       time.Duration
	}{
		{"overloaded", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			ReasonBusy, "(stream overloaded_error: Overloaded)", busyFirst},
		{"rate limit", `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited. Please try again in 20s."}}`,
			ReasonBusy, "(stream rate_limit_error: Rate limited. Please try again in 20s.)", 20 * time.Second},
		{"quota", `{"type":"error","error":{"type":"rate_limit_error","message":"Your credit balance is too low"}}`,
			ReasonQuota, "(stream rate_limit_error: Your credit balance is too low)", quotaPause},
		{"other", `{"type":"error","error":{"type":"api_error","message":"boom"}}`, "", "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			clk := fakeClock(t)
			u := newUpstream(t, answer(200, sse(evStart, evBlock(0, "text"), evText(0, "par"), "error "+c.event),
				"content-type", "text/event-stream"))
			cl := u.client("k")
			var errs [2]error
			for i := range errs {
				_, errs[i] = cl.Complete(context.Background(), "s", "u", 10)
			}
			p := Held(cl.Config())
			if c.reason == "" {
				if u.sent.Load() != 2 || p != nil || IsPaused(errs[0]) || errs[1] == nil || errs[1].Error() != "ai: error: api_error: boom" {
					t.Fatalf("sent %d, held %+v, errs %v", u.sent.Load(), p, errs)
				}
				return
			}
			if !IsPaused(errs[0]) || !IsPaused(errs[1]) || Transient(errs[0]) || u.sent.Load() != 1 {
				t.Fatalf("sent %d, errs %v", u.sent.Load(), errs)
			}
			if p == nil || p.Reason != c.reason || !p.Until.Equal(now().Add(c.pause)) {
				t.Fatalf("held = %+v, want %s for %s", p, c.reason, c.pause)
			}
			if logs := clk.lines(); len(logs) != 1 || !strings.Contains(logs[0], c.detail) {
				t.Fatalf("logs = %q", logs)
			}
		})
	}
}

// message_delta's usage carries totals of only some fields, and some relays send the others as 0 there: a zero does
// not wipe the count message_start gave.
func TestMessagesStreamUsageKeepsStartCounts(t *testing.T) {
	reply, _, err, _, _ := messagesStream(t, sse(evStart, evBlock(0, "text"), evText(0, "x"),
		`message_delta {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"cache_read_input_tokens":0,"output_tokens":7}}`, evStop))
	want := Usage{InputTokens: 120, OutputTokens: 7, CacheReadInputTokens: 30, CacheCreationInputTokens: 5}
	if err != nil || reply.Usage != want {
		t.Fatalf("usage %+v, err %v; want %+v", reply.Usage, err, want)
	}
}

// Any other Messages error after the 200, in the stream or as a whole JSON body a gateway sent with 200, is the error
// it reports, read leniently (an object or a string, with or without the event's own type), and Transient exactly
// when the HTTP status its type stands for would be: api_error (500) and timeout_error (504) are tried again as
// they were before streaming, when they came as the status itself.
func TestMessagesErrorsAfterThe200(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
		transient        bool
	}{
		{"api_error event", sse(evStart, `error {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`),
			"ai: error: api_error: Internal server error", true},
		{"timeout_error event", sse(evStart, `error {"type":"error","error":{"type":"timeout_error","message":"upstream timed out"}}`),
			"ai: error: timeout_error: upstream timed out", true},
		{"invalid_request_error event", sse(evStart, `error {"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`),
			"ai: error: invalid_request_error: bad", false},
		{"error as a string, no type", "data: {\"error\":\"upstream failed\"}\n\n", "ai: error: error: upstream failed", false},
		{"message at the top", "data: {\"type\":\"error\",\"message\":\"boom\"}\n\n", "ai: error: error: boom", false},
		{"object without the event type", "data: {\"error\":{\"type\":\"api_error\",\"message\":\"oops\"}}\n\n", "ai: error: api_error: oops", true},
		{"whole JSON error body", `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`,
			"ai: error: api_error: Internal server error", true},
		{"whole JSON error, no type", `{"error":{"type":"invalid_request_error","message":"bad"}}`,
			"ai: error: invalid_request_error: bad", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			reply, answered, err, _, _ := messagesStream(t, c.body)
			if err == nil || err.Error() != c.want || Transient(err) != c.transient || !answered || reply != (Reply{}) {
				t.Fatalf("reply %+v, answered %v, err %v (transient %v); want %q, transient %v", reply, answered, err, Transient(err), c.want, c.transient)
			}
		})
	}
}

// A limit reported in a whole JSON error body sent with 200, or in an error event without its type, holds the
// connection like the stream's error event.
func TestMessagesLimitsInOtherShapesPause(t *testing.T) {
	for name, c := range map[string]struct{ body, ctype string }{
		"whole JSON body":    {`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "application/json"},
		"event without type": {"data: {\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n", "text/event-stream"},
	} {
		t.Run(name, func(t *testing.T) {
			fakeClock(t)
			u := newUpstream(t, answer(200, c.body, "content-type", c.ctype))
			cl := u.client("k")
			var errs [2]error
			for i := range errs {
				_, errs[i] = cl.Complete(context.Background(), "s", "u", 10)
			}
			if p := Held(cl.Config()); p == nil || p.Reason != ReasonBusy || !IsPaused(errs[0]) || !IsPaused(errs[1]) || u.sent.Load() != 1 {
				t.Fatalf("sent %d, held %+v, errs %v", u.sent.Load(), p, errs)
			}
		})
	}
}
