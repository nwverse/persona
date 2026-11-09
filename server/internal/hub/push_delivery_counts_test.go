package hub

import (
	"testing"
	"time"
)

// This fixture also compiles on the pre-fix hub: it exposes duplicate delivery
// without using production conversations, credentials or an external APNs service.
func TestPushSameInstallationDeliveryCount(t *testing.T) {
	h, sink := testLive(t)
	old := Device{DeviceID: "old-installation-id", CredentialID: "fixture-owner", Credential: "fixture-owner", Kind: "iphone", APNSToken: "old-target", Topic: "app.example", APNSEnv: "production", PushKey: "fixture-same-key"}
	if err := h.push.register(old); err != nil {
		t.Fatal(err)
	}
	newer := old
	newer.DeviceID, newer.APNSToken = "current-installation-id", "current-target"
	if err := h.push.register(newer); err != nil {
		t.Fatal(err)
	}
	h.push.notifyDone("fixture-machine", "Fixture", "fixture-session", "Fixture", "Done", nil)
	sink.calls(t, 2) // this fixture's ordinary phone and the canonical installation
	time.Sleep(100 * time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	attempts := 0
	for _, call := range sink.got {
		if call.Token == "old-target" || call.Token == "current-target" {
			attempts++
		}
	}
	t.Logf("one logical alert: same-installation mock APNs requests=%d", attempts)
	if attempts != 1 {
		t.Fatalf("same installation received %d mock APNs requests, want one", attempts)
	}
}
