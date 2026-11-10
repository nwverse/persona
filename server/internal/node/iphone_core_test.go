package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/iphonectl"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// bridgePhone is an iphoneHost with one paired iPhone whose bridge answers the wire as the device bridge does: a
// screen with a search field and a Pay button, screenshots written to out_file (or inline), inputs taken.
type bridgePhone struct {
	fakeIPhones
	inline bool             // answer screenshots inline rather than in out_file
	fail   map[string]error // the session's error for an op
	mu     sync.Mutex
	ops    []string
}

const bridgePhoneTree = `{
  "app": {"bundle_id": "com.example.shop", "name": "Shop"},
  "point_width": 390, "point_height": 844,
  "root": {"role": "application", "label": "Shop", "frame": [0, 0, 390, 844], "children": [
    {"role": "searchField", "label": "Search", "frame": [16, 100, 358, 36], "enabled": true},
    {"role": "button", "label": "Pay", "frame": [16, 700, 358, 50], "enabled": true}
  ]}
}`

const bridgePhoneInfo = `{"name": "Phone", "product_type": "iPhone16,2", "os_version": "18.1", "point_width": 390,
  "point_height": 844, "orientation": "portrait", "locked": false,
  "capabilities": {"screenshot": true, "ax_tree": true, "input": true, "apps": true}}`

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func newBridgePhone() *bridgePhone {
	return &bridgePhone{fakeIPhones: fakeIPhones{installed: true, links: []iphoneLink{{ID: "d1", Paired: true, State: "connected"}}}}
}

func (b *bridgePhone) Call(_ context.Context, device, op string, args json.RawMessage) (json.RawMessage, error) {
	b.mu.Lock()
	b.ops = append(b.ops, op)
	b.mu.Unlock()
	if device != "d1" {
		return nil, &rpc.Error{Code: "not_paired", Message: "no record"}
	}
	if err := b.fail[op]; err != nil {
		return nil, err
	}
	switch op {
	case "device_info":
		return json.RawMessage(bridgePhoneInfo), nil
	case "ax_tree":
		return json.RawMessage(bridgePhoneTree), nil
	case "screenshot":
		png, _ := base64.StdEncoding.DecodeString(onePixelPNG)
		var a struct {
			OutFile string `json:"out_file"`
		}
		_ = json.Unmarshal(args, &a)
		if b.inline || a.OutFile == "" {
			out, _ := json.Marshal(map[string]any{"mime": "image/png", "data": png, "width": 1, "height": 1})
			return out, nil
		}
		if err := os.WriteFile(a.OutFile, png, 0o600); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"mime":"image/png","width":1,"height":1}`), nil
	}
	return json.RawMessage(`{}`), nil
}

// inputs are the operations that change the phone, in order.
func (b *bridgePhone) inputs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, op := range b.ops {
		if !iphoneReadOnly[op] {
			out = append(out, op)
		}
	}
	return out
}

func useCore(t *testing.T, b *bridgePhone) {
	t.Helper()
	useIPhones(t, &b.fakeIPhones, nil)
	oldHost, oldCore := iphones, iphoneCore
	iphones, iphoneCore = b, &iphoneControl{}
	t.Cleanup(func() { iphones, iphoneCore = oldHost, oldCore })
}

func TestIPhoneCoreLooksThroughTheBridge(t *testing.T) {
	for _, inline := range []bool{false, true} {
		b := newBridgePhone()
		b.inline = inline
		useCore(t, b)
		res, err := iphoneCall(context.Background(), json.RawMessage(`{"session":"s1","op":"look","args":{"screenshot":true}}`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Text, "Pay") || !strings.Contains(res.Text, "Search") || !json.Valid(res.Output) {
			t.Fatalf("inline %v: look %q %s", inline, res.Text, res.Output)
		}
		if res.Image == nil || res.Image.Data == "" || res.Image.Width != 1 {
			t.Fatalf("inline %v: screenshot %+v", inline, res.Image)
		}
		if len(b.inputs()) != 0 {
			t.Fatalf("a look sent input: %v", b.inputs())
		}
	}
}

func TestIPhoneCoreAsksBeforeAPaymentAndResumesWithTheApproval(t *testing.T) {
	b := newBridgePhone()
	useCore(t, b)
	ctx := context.Background()
	if _, err := iphoneCall(ctx, json.RawMessage(`{"session":"s1","op":"look","args":{"screenshot":false}}`)); err != nil {
		t.Fatal(err)
	}
	do := `{"session":"s1","op":"do","args":{"steps":[{"action":"tap","target":{"label":"Pay"}}]}}`
	res, err := iphoneCall(ctx, json.RawMessage(do))
	if err != nil {
		t.Fatal(err)
	}
	if !res.NeedsApproval || res.ApprovalID == "" || res.ApprovalSummary != "" {
		t.Fatalf("a payment goes ahead: %+v", res)
	}
	var a iphonectl.Approval
	if err := json.Unmarshal(res.Approval, &a); err != nil || a.Step != 1 || a.Action != "tap" ||
		!slices.Contains(a.Needs, iphonectl.Payment) || a.App.BundleID != "com.example.shop" || a.Target == nil {
		t.Fatalf("approval %s (%v)", res.Approval, err)
	}
	if strings.Contains(res.Text, res.ApprovalID) || strings.Contains(string(res.Output), res.ApprovalID) {
		t.Fatal("the approval id reaches the model")
	}
	if len(b.inputs()) != 0 {
		t.Fatalf("sent before approval: %v", b.inputs())
	}

	// The manager comes back with the id once the owner approved: the step is sent, once.
	var call proto.IPhoneCall
	_ = json.Unmarshal([]byte(do), &call)
	call.Approved = res.ApprovalID
	raw, _ := json.Marshal(call)
	res, err = iphoneCall(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsApproval || strings.Join(b.inputs(), ",") != "tap" {
		t.Fatalf("after approval: %+v, inputs %v", res, b.inputs())
	}
	// The id is spent.
	if res, err = iphoneCall(ctx, raw); err != nil || strings.Join(b.inputs(), ",") != "tap" {
		t.Fatalf("approved twice: %+v %v, inputs %v", res, err, b.inputs())
	}
}

func TestIPhoneCoreConfirmAsksBeforeAnyInput(t *testing.T) {
	b := newBridgePhone()
	useCore(t, b)
	ctx := context.Background()
	if _, err := iphoneCall(ctx, json.RawMessage(`{"session":"s1","op":"look","args":{"screenshot":false}}`)); err != nil {
		t.Fatal(err)
	}
	res, err := iphoneCall(ctx, json.RawMessage(`{"session":"s1","op":"do","confirm":true,
		"args":{"steps":[{"action":"tap","target":{"label":"Search"}},{"action":"type","text":"hello"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var a iphonectl.Approval
	if !res.NeedsApproval || json.Unmarshal(res.Approval, &a) != nil || !slices.Contains(a.Needs, iphonectl.Confirm) || len(a.Steps) != 2 {
		t.Fatalf("confirm: %+v %s", res, res.Approval)
	}
	if len(b.inputs()) != 0 {
		t.Fatalf("sent before approval: %v", b.inputs())
	}
}

func TestIPhoneCoreKeepsWhetherAnInputWasSent(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		err  error
		want iphonectl.Delivery
	}{
		// The session refused before writing anything, with a code the bridge doesn't have.
		{"session refused", &rpc.Error{Code: "not_paired", Message: "no record", Data: json.RawMessage(`{"sent":"no"}`)}, iphonectl.DeliveryNotSent},
		{"bridge refused", &rpc.Error{Code: "locked", Message: "locked"}, iphonectl.DeliveryNotSent},
		// Handed to the bridge, then the link dropped: it may have happened.
		{"uncertain", &rpc.Error{Code: "uncertain", Message: "link lost", Data: json.RawMessage(`{"sent":"unknown"}`)}, iphonectl.DeliveryUnknown},
		{"refusal code but maybe sent", &rpc.Error{Code: "locked", Message: "x", Data: json.RawMessage(`{"sent":"unknown"}`)}, iphonectl.DeliveryUnknown},
		{"no code", errors.New("bridge crashed"), iphonectl.DeliveryUnknown},
	} {
		b := newBridgePhone()
		b.fail = map[string]error{"tap": c.err}
		useCore(t, b)
		if _, err := iphoneCall(ctx, json.RawMessage(`{"session":"s1","op":"look","args":{"screenshot":false}}`)); err != nil {
			t.Fatal(err)
		}
		res, err := iphoneCall(ctx, json.RawMessage(`{"session":"s1","op":"do","args":{"steps":[{"action":"tap","target":{"label":"Search"}},{"action":"type","text":"hi"}]}}`))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var out iphonectl.DoResult
		if err := json.Unmarshal(res.Output, &out); err != nil || len(out.Steps) == 0 {
			t.Fatalf("%s: %s (%v)", c.name, res.Output, err)
		}
		if out.Steps[0].Delivery != c.want {
			t.Errorf("%s: tap %s, want %s", c.name, out.Steps[0].Delivery, c.want)
		}
		// Nothing after a failed input goes, and a tap that may have happened is never sent again.
		if got := strings.Join(b.inputs(), ","); got != "tap" {
			t.Errorf("%s: inputs %s", c.name, got)
		}
	}
}

func TestIPhoneWireCallKeepsTheCode(t *testing.T) {
	ctx := context.Background()
	call := iphoneWireCall(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, &rpc.Error{Code: "busy", Message: "pairing", Data: json.RawMessage(`{"sent":"no"}`)}
	})
	_, err := call(ctx, "tap", nil)
	var e *iphonectl.Error
	if !errors.As(err, &e) || e.Code != "busy" || e.Message != "pairing" || !e.NotSent {
		t.Fatalf("%#v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	call = iphoneWireCall(func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) { return nil, ctx.Err() })
	if _, err := call(cancelled, "tap", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("the context's error: %v", err)
	}
}
