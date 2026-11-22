package extension

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEntriesPersonalReadUsesInstalledManifestAndLiveGrant(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, nil, 1)
	for _, tc := range []struct {
		id      string
		declare bool
		grant   Permissions
		want    bool
	}{
		{"approved", true, Permissions{Hub: []string{ScopeAssistantEntriesRead}}, true},
		{"unapproved", true, Permissions{}, false},
		{"undeclared", false, Permissions{Hub: []string{ScopeAssistantEntriesRead}}, false},
		{"legacy", false, Permissions{HubAPI: true}, false},
	} {
		permissions := Permissions{}
		if tc.declare {
			permissions.Hub = []string{ScopeAssistantEntriesRead}
		}
		m := &Manifest{ID: tc.id, Version: "1.0.0", Permissions: permissions, Contributes: map[string]json.RawMessage{}}
		if err := r.AddBuiltin(m, t.TempDir(), true); err != nil {
			t.Fatal(err)
		}
		r.mu.Lock()
		r.extensions[tc.id].Record.Granted = tc.grant
		r.mu.Unlock()
		_, personal := r.EntriesReadAccess(tc.id)
		if personal != tc.want {
			t.Errorf("%s: personal=%v", tc.id, personal)
		}
	}
	if err := r.SetEnabled(context.Background(), "approved", false, "test"); err != nil {
		t.Fatal(err)
	}
	if allowed, personal := r.EntriesReadAccess("approved"); allowed || personal {
		t.Fatal("disabled extension retained access")
	}
	if err := r.SetEnabled(context.Background(), "approved", true, "test"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.extensions["approved"].Record.Granted = Permissions{}
	r.mu.Unlock()
	if allowed, personal := r.EntriesReadAccess("approved"); allowed || personal {
		t.Fatal("revoked scope retained access")
	}
}
