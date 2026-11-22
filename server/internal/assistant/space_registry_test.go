package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// spaceRegistryStore is a memStore with a registry (assistant_spaces) of its own.
type spaceRegistryStore struct {
	*memStore
	rows []db.Space
}

func (st *spaceRegistryStore) Spaces(context.Context) ([]db.Space, error) { return st.rows, nil }

// The registry and the policy (#13104): the main space turns nothing on, the health space everything, and a slug the
// registry does not know is as strict as the health space.
func TestSpacePolicy(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFakeHub())
	all := strictPolicy
	if got := s.spacePolicy(ctx, ""); got != (SpacePolicy{}) {
		t.Errorf("main = %+v, want all off", got)
	}
	if got := s.spacePolicy(ctx, SpaceHealth); got != all {
		t.Errorf("health = %+v, want all on", got)
	}
	if got := s.spacePolicy(ctx, "qa-unknown"); got != all {
		t.Errorf("an unregistered slug = %+v, want strict", got)
	}
	if !s.spaceKnown(ctx, "") || !s.spaceKnown(ctx, SpaceHealth) || s.spaceKnown(ctx, "qa-unknown") {
		t.Error("spaceKnown: want the main and health spaces only")
	}
}

// Registered spaces (#13104): every one other than the main space is strict until the per-use grading lands, the
// built-in row cannot change the health space, and an archived space is no longer one to name while its conversations
// stay as strict as they were.
func TestSpacePolicyFromRegistry(t *testing.T) {
	ctx := context.Background()
	st := &spaceRegistryStore{memStore: &memStore{}, rows: []db.Space{
		{Slug: "health", Template: spaceTemplateSpace, DirName: "elsewhere", Builtin: true},
		{Slug: "qa-open", Template: spaceTemplateSpace, DirName: "spaces/qa-open"},
	}}
	s := newServiceOn(t, newFakeHub(), st)
	if e, ok := s.spaceEntryOf(ctx, SpaceHealth); !ok || e.Template != spaceTemplateHealth || e.DirName != "health" || !e.Builtin {
		t.Errorf("health = %+v (%v), want the built-in entry", e, ok)
	}
	if got := s.spacePolicy(ctx, "qa-open"); got != strictPolicy {
		t.Errorf("qa-open = %+v, want strict", got)
	}
	if !s.spaceKnown(ctx, "qa-open") {
		t.Error("qa-open is registered")
	}
	gone := time.Unix(1_800_000_000, 0)
	st.rows = append(st.rows, db.Space{Slug: "qa-gone", Template: spaceTemplateSpace, ArchivedAt: &gone})
	s.spacesChanged()
	if s.spaceKnown(ctx, "qa-gone") || !s.spaceKnown(ctx, "qa-open") {
		t.Error("spaceKnown: an archived space is not known, a live one is")
	}
	if got := s.spacePolicy(ctx, "qa-gone"); got != strictPolicy {
		t.Errorf("an archived space = %+v, want strict", got)
	}
}
