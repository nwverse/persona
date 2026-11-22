package hub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestHubExtensionMachinesPairLinkAndStatus(t *testing.T) {
	h, _, _ := addHub(t)
	h.cfg.PublicURL = "https://hub.example.com"
	// Clean up test machine ID if leftover in shared test DB.
	_ = h.trustStore().ForgetMachineTrust(context.Background(), "ext-cloud-box")
	_, _ = h.revokeComputer(context.Background(), "ext-cloud-box")

	// 1. Status before adding: exists=false.
	st, err := h.hubMachinesStatus(json.RawMessage(`{"machine_id":"ext-cloud-box"}`))
	if err != nil {
		t.Fatalf("status before add: %v", err)
	}
	if st.Exists || st.Online || st.Pending {
		t.Fatalf("unexpected status before add: %+v", st)
	}

	// 2. Request a pairing link via hubMachinesPairLink.
	pairRes, err := h.hubMachinesPairLink(context.Background(), "", "personal-agent-club",
		json.RawMessage(`{"machine_id":"ext-cloud-box","name":"Cloud Box","os":"linux"}`))
	if err != nil {
		t.Fatalf("pair_link: %v", err)
	}
	if pairRes.MachineID != "ext-cloud-box" || pairRes.Name != "Cloud Box" || !pairRes.Pending {
		t.Fatalf("unexpected pair_link result: %+v", pairRes)
	}
	if !strings.HasPrefix(pairRes.Link, "persona://link?") || !strings.Contains(pairRes.Link, "machine=ext-cloud-box") {
		t.Fatalf("unexpected pair link: %q", pairRes.Link)
	}

	// 3. Status after adding: exists=true, pending=true, online=false.
	st, err = h.hubMachinesStatus(json.RawMessage(`{"machine_id":"ext-cloud-box"}`))
	if err != nil {
		t.Fatalf("status after add: %v", err)
	}
	if !st.Exists || !st.Pending || st.Online || st.Name != "Cloud Box" {
		t.Fatalf("unexpected status after add: %+v", st)
	}

	// 4. Re-issuing for an already-confirmed computer (onb-own) is refused with not_pending.
	_, err = h.hubMachinesPairLink(context.Background(), "", "personal-agent-club",
		json.RawMessage(`{"machine_id":"onb-own"}`))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != "not_pending" {
		t.Fatalf("confirmed computer pair_link: got %v, want rpc.Error(not_pending)", err)
	}

	// 5. Via hub extension bus dispatch (extwire.HubMachinesStatus).
	rawOut, handled, err := h.extAppCall(context.Background(), "", assistantwire.AppCall{
		AppID:  extwire.AppHub,
		Method: extwire.HubMachinesStatus,
		Params: json.RawMessage(`{"extension":"personal-agent-club","request":{"machine_id":"ext-cloud-box"}}`),
	})
	if !handled || err != nil {
		t.Fatalf("bus dispatch: handled=%v err=%v", handled, err)
	}
	if !strings.Contains(string(rawOut), `"exists":true`) || !strings.Contains(string(rawOut), `"pending":true`) {
		t.Fatalf("unexpected bus output: %s", rawOut)
	}
}
