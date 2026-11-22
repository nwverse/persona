package hub

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The pairing receipts' payloads (docs/protocol/protocol.md "Computer pairing codes"): the category picks the buttons, `pair` carries
// the ids they act on, and the alert text never has an id.
func TestPairReceiptPayloads(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  map[string]any
		want string
	}{
		{"computer_code", computerCodePayload(i18n.En, "iPhone", "Office Mac", "cred_issuer", "mach_office"),
			`{"aps":{"alert":{"body":"iPhone made a pairing code for the computer “Office Mac”","title":"Persona"},"category":"pair_receipt","sound":"default"},` +
				`"pair":{"device_id":"cred_issuer","kind":"computer_code","machine":"mach_office"}}`},
		{"computer_joining", computerJoiningPayload(i18n.En, "Office Mac", "mach_office"),
			`{"aps":{"alert":{"body":"A new computer, “Office Mac”, wants to join. Open the app to confirm it’s yours","title":"Persona"},"category":"computer_joining","sound":"default"},` +
				`"pair":{"kind":"computer_joining","machine":"mach_office"}}`},
		{"device", devicePairedPayload(i18n.En, "iPad", "cred_new"),
			`{"aps":{"alert":{"body":"New device paired: iPad","title":"Persona"},"category":"pair_receipt","sound":"default"},` +
				`"pair":{"device_id":"cred_new","kind":"device"}}`},
	} {
		b, _ := json.Marshal(tc.got)
		if string(b) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, b, tc.want)
		}
		alert, _ := json.Marshal(tc.got["aps"].(map[string]any)["alert"])
		for _, id := range []string{"cred_", "mach_"} {
			if bytes.Contains(alert, []byte(id)) {
				t.Errorf("%s: alert has an id: %s", tc.name, alert)
			}
		}
	}
}

// A device without a name is not named by its credential id in the receipt.
func TestComputerCodeReceiptNeverNamesTheCredential(t *testing.T) {
	h, f := testLive(t)
	h.notifyComputerCode(principal{ID: "cred_issuer"}, "mach_office")
	var p map[string]any
	if err := json.Unmarshal(f.calls(t, 1)[0].Body, &p); err != nil {
		t.Fatal(err)
	}
	aps := p["aps"].(map[string]any)
	body, _ := aps["alert"].(map[string]any)["body"].(string)
	if strings.Contains(body, "cred_issuer") || body == "" {
		t.Fatalf("alert body %q", body)
	}
	pair, _ := p["pair"].(map[string]any)
	if aps["category"] != pairCategoryReceipt || pair["kind"] != "computer_code" || pair["device_id"] != "cred_issuer" || pair["machine"] != "mach_office" {
		t.Fatalf("payload %v", p)
	}
}

// Through the relay the category and `pair` are sealed with the rest (the extension puts the category on the
// notification and keeps `pair` in userInfo); the relay sees neither the ids nor the category.
func TestRelayPairReceiptsAreSealed(t *testing.T) {
	h, f, priv := relayHub(t)
	h.notifyComputerCode(principal{ID: "cred_issuer", Name: "iPhone"}, "mach_office")
	h.notifyComputerJoining("mach_office")
	h.notifyPairing("iPad", "cred_new")
	got := f.wait(t, 3)
	byKind := map[string]map[string]any{}
	for _, r := range got {
		for _, leak := range []string{"cred_", "mach_", "pair_receipt", "computer_joining", "iPad", "iPhone"} {
			if bytes.Contains(r.Body, []byte(leak)) {
				t.Fatalf("relay saw %q: %s", leak, r.Body)
			}
		}
		p := opened(t, priv, r)
		pair, _ := p["pair"].(map[string]any)
		kind, _ := pair["kind"].(string)
		byKind[kind] = p
	}
	for kind, want := range map[string]struct {
		category string
		pair     map[string]any
	}{
		"computer_code":    {pairCategoryReceipt, map[string]any{"kind": "computer_code", "device_id": "cred_issuer", "machine": "mach_office"}},
		"computer_joining": {pairCategoryJoining, map[string]any{"kind": "computer_joining", "machine": "mach_office"}},
		"device":           {pairCategoryReceipt, map[string]any{"kind": "device", "device_id": "cred_new"}},
	} {
		p, ok := byKind[kind]
		if !ok {
			t.Fatalf("no %s push among %d", kind, len(got))
		}
		if cat := p["aps"].(map[string]any)["category"]; cat != want.category {
			t.Errorf("%s: category %v", kind, cat)
		}
		gotPair, _ := json.Marshal(p["pair"])
		wantPair, _ := json.Marshal(want.pair)
		if !bytes.Equal(gotPair, wantPair) {
			t.Errorf("%s: pair %s, want %s", kind, gotPair, wantPair)
		}
	}
}
