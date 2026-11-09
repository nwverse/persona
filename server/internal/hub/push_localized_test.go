package hub

// An extension's hub.push_localized carries one payload per language it wrote: a device in a language it left out gets
// another of them, an empty one is refused, and a push built with nothing for a language skips that language's devices
// instead of panicking the hub (an app.call runs on the bus handler's goroutine, which nothing recovers).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// localizedHub is testLive's hub with its phone in Japanese.
func localizedHub(t *testing.T) (*Hub, *fakeAPNs) {
	t.Helper()
	h, f := testLive(t)
	if err := h.push.register(Device{DeviceID: "phone", Name: "phone", APNSToken: "devtok", Topic: "app.example", Locale: "ja"}); err != nil {
		t.Fatal(err)
	}
	return h, f
}

// pushLocalized calls hub.push_localized as the assistant's manager does, failing the test (not the binary) on a panic.
func pushLocalized(t *testing.T, h *Hub, params string) error {
	t.Helper()
	return hubAppCall(t, h, extwire.HubPushLocalized, params)
}

func hubAppCall(t *testing.T, h *Hub, method, params string) error {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s %s panicked: %v", method, params, r)
		}
	}()
	_, ok, err := h.extAppCall(context.Background(), assistantwire.AssistantID,
		assistantwire.AppCall{AppID: extwire.AppHub, Method: method, Params: json.RawMessage(params)})
	if !ok {
		t.Fatalf("%s %s: not the hub's method", method, params)
	}
	return err
}

// noMoreCalls waits a moment and fails if APNs got more than want pushes.
func noMoreCalls(t *testing.T, f *fakeAPNs, want int) []apnsCall {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != want {
		t.Fatalf("APNs got %d pushes, want %d: %+v", len(f.got), want, f.got)
	}
	return append([]apnsCall{}, f.got...)
}

func TestPushLocalizedFallsBackForAMissingLanguage(t *testing.T) {
	h, f := localizedHub(t)
	if err := pushLocalized(t, h, `{"payloads":{"en":{"aps":{"alert":{"title":"English title","body":"English body"}}}}}`); err != nil {
		t.Fatalf("push.localized with only en: %v", err)
	}
	f.calls(t, 1)
	got := noMoreCalls(t, f, 1)
	if got[0].Token != "devtok" || !strings.Contains(string(got[0].Body), "English title") {
		t.Fatalf("the Japanese phone got %s %s, want the English alert", got[0].Token, got[0].Body)
	}
}

func TestPushLocalizedPrefersFallbackThenDefault(t *testing.T) {
	payloads := func(langs ...string) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, l := range langs {
			out[l] = map[string]any{"lang": l}
		}
		return out
	}
	for _, c := range []struct {
		langs []string
		lang  i18n.Lang
		want  string
	}{
		{[]string{"ja", "en", "zh-Hans"}, i18n.Ja, "ja"},
		{[]string{"en", "zh-Hans"}, i18n.Ja, "en"},
		{[]string{"zh-Hans", "xx"}, i18n.Ja, "zh-Hans"},
		{[]string{"yy", "xx"}, i18n.Ja, "xx"},
	} {
		got := localizedPayload(payloads(c.langs...), c.lang)
		if got["lang"] != c.want {
			t.Errorf("%v for %s: got %v, want %s", c.langs, c.lang, got, c.want)
		}
	}
	if got := localizedPayload(map[string]map[string]any{"en": nil, "ja": nil}, i18n.Ja); got != nil {
		t.Errorf("only null payloads: got %v, want none", got)
	}
}

func TestPushLocalizedWithoutPayloadsIsRefused(t *testing.T) {
	for _, params := range []string{`{}`, `{"payloads":{}}`, `{"payloads":{"en":null}}`} {
		h, f := localizedHub(t)
		if err := pushLocalized(t, h, params); err == nil {
			t.Errorf("push.localized %s: no error", params)
		}
		noMoreCalls(t, f, 0)
	}
}

func TestSendFromSkipsALanguageWithoutPayload(t *testing.T) {
	h, f := localizedHub(t)
	if err := h.push.register(Device{DeviceID: "tablet", Name: "tablet", APNSToken: "tok-tablet", Topic: "app.example", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	build := func(l i18n.Lang) map[string]any {
		if l != i18n.En {
			return nil
		}
		return map[string]any{"aps": map[string]any{"alert": map[string]any{"title": "only en"}}}
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("sendFrom with a nil payload panicked: %v", r)
			}
		}()
		h.push.sendFrom("", build, "", nil, quietNone, nil)
	}()
	f.calls(t, 1)
	if got := noMoreCalls(t, f, 1); got[0].Token != "tok-tablet" {
		t.Fatalf("the push went to %s, want only the English tablet", got[0].Token)
	}

	// Nothing in any language: no push at all.
	h, f = localizedHub(t)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("sendFrom with no payload panicked: %v", r)
			}
		}()
		h.push.sendFrom("", func(i18n.Lang) map[string]any { return nil }, "", nil, quietNone, nil)
	}()
	noMoreCalls(t, f, 0)
}

// push.background without a payload reaches sendBuiltWith with nil too (sendAs): no panic, no push.
func TestPushBackgroundWithoutPayloadSendsNothing(t *testing.T) {
	h, f := localizedHub(t)
	if err := hubAppCall(t, h, extwire.HubPushBackground, `{"devices":["phone"]}`); err != nil {
		t.Fatalf("push.background without a payload: %v", err)
	}
	noMoreCalls(t, f, 0)
}
