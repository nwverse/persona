package larkcli

import (
	"testing"

	adapter "github.com/nwverse/persona/server/internal/larkcli"
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
		{"another person in role row", `{"elements":[{"tag":"plain_text","text":"On-call:"},{"tag":"at","user_id":"ou_someone_else"}]}`, false},
		{"ordinary card mention", `{"elements":[{"tag":"at","user_id":"ou_example_owner"}]}`, false},
		{"ordinary card person", `{"elements":[{"tag":"person","user_id":"ou_example_owner"}]}`, false},
		{"role does not leak past text", `{"elements":[{"tag":"plain_text","text":"Owner:"},{"tag":"plain_text","text":"Other Person"},{"tag":"at","user_id":"ou_example_owner"}]}`, false},
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
