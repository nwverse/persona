package larkcli

import (
	"context"
	"encoding/json"
	"testing"

	adapter "github.com/nwverse/persona/server/internal/modules/lark/larkcli"
)

func TestCardAssignsOwnerRequiresResponsibilityRole(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", UserName: "Example Owner"}
	cases := []struct {
		name, content string
		want          bool
	}{
		{"exact on-call field", `"On-call: Example Owner"`, true},
		{"exact responsible field", `"Responsible person: @Example Owner"`, true},
		{"another responsible person", `"Owner: Other Person"`, false},
		{"name outside role", `"Incident reported by Example Owner"`, false},
		{"name prefix", `"Owner: Example Owner Assistant"`, false},
		{"person in role row", `{"elements":[{"tag":"plain_text","text":"Owner:"},{"tag":"person","user_id":"ou_example_owner"}]}`, true},
		{"at in role row", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"at","user_id":"ou_example_owner"}]}`, true},
		{"nested content role label", `{"elements":[{"tag":"div","text":{"tag":"plain_text","content":"On-call:"}},{"tag":"person","user_id":"ou_example_owner"}]}`, true},
		{"multiple assigned identities", `{"elements":[{"tag":"plain_text","content":"Owner:"},{"tag":"at","user_id":"ou_someone_else"},{"tag":"at","user_id":"ou_example_owner"}]}`, true},
		{"another person in role row", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"at","user_id":"ou_someone_else"}]}`, false},
		{"ordinary card mention", `{"elements":[{"tag":"at","user_id":"ou_example_owner"}]}`, false},
		{"ordinary card person", `{"elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, false},
		{"role does not leak past text", `{"elements":[{"tag":"plain_text","text":"Owner:"},{"tag":"plain_text","text":"Other Person"},{"tag":"at","user_id":"ou_example_owner"}]}`, false},
		{"completed other field does not cover later mention", `{"elements":[{"tag":"plain_text","content":"Owner: Other Person"},{"tag":"at","user_id":"ou_example_owner"}]}`, false},
		{"review fixture assigns a peer before mentioning owner", `{"elements":[{"tag":"plain_text","content":"On-call: Example Peer"},{"tag":"at","user_id":"ou_example_owner"}]}`, false},
		{"completed container label does not cover unrelated mention", `{"tag":"div","text":{"tag":"plain_text","content":"Owner: Other Person"},"elements":[{"tag":"at","user_id":"ou_example_owner"}]}`, false},
		{"empty container label covers its assigned person", `{"tag":"div","text":{"tag":"plain_text","content":"Owner:"},"elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, true},
		{"mention all", `{"elements":[{"tag":"plain_text","text":"Owner:"},{"tag":"at","user_id":"all"}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := addressedMessage("om_example_card", "cli_example_monitor", "Example Monitor", tc.content, "", nil)
			message.MsgType = "interactive"
			if got := cardAssignsOwner(message, auth); got != tc.want {
				t.Fatalf("cardAssignsOwner = %v, want %v", got, tc.want)
			}
			if tc.want && !cardAddressesOwner(message, auth) {
				t.Fatal("an explicit responsibility card was not addressed")
			}
			message.MsgType = "text"
			if cardAssignsOwner(message, auth) {
				t.Fatal("non-card text gained the sender-mute exception")
			}
		})
	}
}

func TestCardPeerAssignmentAndOwnerMentionDoesNotGainMuteException(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", AppID: "cli_example_bot", UserName: "Example Owner"}
	for _, tc := range []struct {
		name, content string
		addressed     bool
	}{
		{"explicit at remains a mention", `{"elements":[{"tag":"plain_text","content":"On-call: Example Peer"},{"tag":"at","user_id":"ou_example_owner"}]}`, true},
		{"unrelated person in row", `{"elements":[{"tag":"plain_text","content":"On-call: Example Peer"},{"tag":"person","user_id":"ou_example_owner"}]}`, false},
		{"unrelated person in container", `{"tag":"div","text":{"tag":"plain_text","content":"On-call: Example Peer"},"elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := addressedMessage("om_example_card", "cli_example_monitor", "Example Monitor", tc.content, "", nil)
			message.MsgType = "interactive"
			c := addressedTestConnector(t, []adapter.Message{message}, []adapter.Message{message})
			groups := c.groupMessages([]adapter.Message{message}, auth, nil)
			if !tc.addressed {
				if len(groups) != 0 {
					t.Fatalf("another person's assignment addressed the owner: %+v", groups)
				}
				return
			}
			if len(groups) != 1 {
				t.Fatalf("the explicit owner mention was lost: %+v", groups)
			}
			thing := c.thingForGroup(context.Background(), groups[0], auth)
			if thing.Tier != "action" || thing.OwnerAssigned {
				t.Fatalf("ordinary owner mention gained the responsibility mute exception: %+v", thing)
			}
		})
	}
}

func TestCardStructuredOwnerIdentityRequiresOpenID(t *testing.T) {
	for _, tag := range []string{"at", "person"} {
		for _, tc := range []struct {
			name, field, ownerID string
			types                map[string]any
			want                 bool
		}{
			{"explicit open ID", "open_id", "ou_example_owner", nil, true},
			{"card user ID defaults to open ID", "user_id", "ou_example_owner", nil, true},
			{"declared open ID", "user_id", "ou_example_owner", map[string]any{"user_id_type": "open_id"}, true},
			{"declared ID type", "user_id", "ou_example_owner", map[string]any{"id_type": "open_id"}, true},
			{"generic element ID", "id", "ou_example_owner", nil, false},
			{"generic element ID with type", "id", "ou_example_owner", map[string]any{"id_type": "open_id"}, false},
			{"employee ID in user field", "user_id", "example_employee", nil, false},
			{"employee ID in open field", "open_id", "example_employee", nil, false},
			{"union ID in user field", "user_id", "on_example_owner", nil, false},
			{"empty open ID suffix", "user_id", "ou_", nil, false},
			{"conflicting user ID type", "user_id", "ou_example_owner", map[string]any{"user_id_type": "user_id"}, false},
			{"conflicting ID type", "user_id", "ou_example_owner", map[string]any{"id_type": "union_id"}, false},
			{"contradictory open ID field", "open_id", "ou_example_owner", map[string]any{"user_id_type": "user_id"}, false},
			{"malformed user ID type", "user_id", "ou_example_owner", map[string]any{"user_id_type": true}, false},
			{"null ID type", "user_id", "ou_example_owner", map[string]any{"id_type": nil}, false},
		} {
			t.Run(tag+"/"+tc.name, func(t *testing.T) {
				auth := adapter.AuthStatus{OpenID: tc.ownerID}
				element := map[string]any{"tag": tag, tc.field: tc.ownerID}
				for key, value := range tc.types {
					element[key] = value
				}
				raw, err := json.Marshal(map[string]any{"elements": []any{
					map[string]any{"tag": "plain_text", "text": "On-call:"}, element,
				}})
				if err != nil {
					t.Fatal(err)
				}
				message := addressedMessage("om_example_card", "cli_example_monitor", "Example Monitor", string(raw), "", nil)
				if got := cardAddressesOwner(message, auth); got != tc.want {
					t.Errorf("addressed=%v want=%v", got, tc.want)
				}
				if got := cardAssignsOwner(message, auth); got != tc.want {
					t.Errorf("assigned=%v want=%v", got, tc.want)
				}
				c := addressedTestConnector(t, []adapter.Message{message}, []adapter.Message{message})
				groups := c.groupMessages([]adapter.Message{message}, auth, nil)
				if !tc.want {
					if len(groups) != 0 {
						t.Fatalf("unverified identity granted Inbox admission: %+v", groups)
					}
					return
				}
				if len(groups) != 1 {
					t.Fatalf("verified identity was not admitted: %+v", groups)
				}
				thing := c.thingForGroup(context.Background(), groups[0], auth)
				if thing.Tier != "action" || !thing.OwnerAssigned {
					t.Fatalf("verified assignment lost its routing or mute exception: %+v", thing)
				}
			})
		}
	}
}

func TestCardStructuredIdentitiesDoNotFallBackToRenderedNames(t *testing.T) {
	auth := adapter.AuthStatus{OpenID: "ou_example_owner", UserName: "Example Owner"}
	for _, tc := range []struct {
		name, content       string
		addressed, assigned bool
	}{
		{"another identity with owner's display name", `{"elements":[{"tag":"person","user_id":"ou_example_peer","text":"On-call: Example Owner"}]}`, false, false},
		{"rendered projection disagrees with assignee", `{"text":"On-call: Example Owner","elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"person","user_id":"ou_example_peer"}]}`, false, false},
		{"completed rendered field cannot lend role", `{"text":"On-call:\nExample Peer","elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, false, false},
		{"invalid structured identity cannot use display name", `{"elements":[{"tag":"person","user_id":"example_employee","text":"On-call: Example Owner"}]}`, false, false},
		{"ordinary at survives conflicting rendered assignment", `{"text":"On-call: Example Owner","elements":[{"tag":"at","user_id":"ou_example_owner"}]}`, true, false},
		{"ordinary person cannot borrow rendered assignment", `{"text":"On-call: Example Owner","elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, false, false},
		{"direct structured assignment uses stable identity", `{"elements":[{"tag":"person","user_id":"ou_example_owner","text":"On-call: Former Name"}]}`, true, true},
		{"structured row uses stable identity", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"person","user_id":"ou_example_owner","text":"Former Name"}]}`, true, true},
		{"text-only structured projection", `{"elements":[{"tag":"plain_text","text":"On-call: Example Owner"}]}`, true, true},
		{"compact rendered projection", `"<card>On-call: Example Owner</card>"`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, nested := range []bool{false, true} {
				raw := json.RawMessage(tc.content)
				if nested {
					var err error
					raw, err = json.Marshal(tc.content)
					if err != nil {
						t.Fatal(err)
					}
				}
				message := adapter.Message{MsgType: "interactive", Content: raw}
				if got := cardAddressesOwner(message, auth); got != tc.addressed {
					t.Errorf("nested=%v addressed=%v want=%v", nested, got, tc.addressed)
				}
				if got := cardAssignsOwner(message, auth); got != tc.assigned {
					t.Errorf("nested=%v assigned=%v want=%v", nested, got, tc.assigned)
				}
			}
		})
	}
}
