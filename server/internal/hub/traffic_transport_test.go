package hub

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

func TestTrafficPushResultsAndCategories(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0)}
	p := &Pusher{traffic: h.recordTraffic}
	d := Device{DeviceID: "private-registration", CredentialID: "private-credential", Name: "private-name", APNSToken: "private-token", Kind: "iphone"}
	for _, event := range []string{"start", "update", "end", "private-event"} {
		payload := map[string]any{"aps": map[string]any{"event": event, "content-state": "private-state"}}
		p.notePush(d, payload, "liveactivity", "apns", "attempt", 0, 100, 0)
		p.noteDirectResult(d, payload, "liveactivity", nil, 200, 100, 10*time.Millisecond)
	}
	p.noteRelayResult(d, nil, "alert", errors.New("private-error"), false, 10, 0)
	p.noteRelayResult(d, nil, "alert", nil, true, 10, 0)
	p.noteDirectResult(d, nil, "alert", nil, 429, 10, 0)
	p.noteRelayResult(d, map[string]any{"extension_widget": map[string]any{"extension": "private-widget", "id": "private-id"}}, "background", nil, false, 10, 0)
	p.noteRelayResult(d, map[string]any{"resolved_interaction_ids": []string{"private-interaction"}}, "background", nil, false, 10, 0)
	h.flushTraffic(time.Now(), true)
	text := output.String()
	for _, private := range []string{"private-registration", "private-credential", "private-name", "private-token", "private-event", "private-state", "private-error", "private-widget", "private-interaction"} {
		if strings.Contains(text, private) {
			t.Fatalf("private value %q entered telemetry", private)
		}
	}
	for _, event := range []string{"liveactivity_start", "liveactivity_update", "liveactivity_end", "liveactivity_unknown", "background_widget", "background_withdraw"} {
		if !strings.Contains(text, "event="+event) {
			t.Fatalf("missing category %s: %s", event, text)
		}
	}
	if strings.Count(text, "action=accepted status=200") != 6 || strings.Count(text, "action=failed") != 3 {
		t.Fatal(text)
	}
	t.Log("Live Activity logical attempts=4, accepts=4; retry-result accounting emits one terminal result per logical send")
}
