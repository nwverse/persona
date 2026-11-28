package assistant

import (
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/repository"
)

// Historical automatic imports and explicit associations normalize delivery to the
// same empty stored role. A later no-PR closeout cannot discard either saved gate.
// This protects the conservative compatibility policy; it does not classify old
// mistaken automatic associations or claim that their cleanup is implemented.
func TestPersistedDefaultDeliverySurvivesNoPRCloseout(t *testing.T) {
	for _, source := range []string{"report", "legacy", "explicit"} {
		t.Run(source, func(t *testing.T) {
			s, todo, forge := linkRoleTask(t)
			ctx := t.Context()
			if err := s.importTodoRepositoryLinks(ctx, todo.ID, []string{ownDeliveryPR}, source); err != nil {
				t.Fatal(err)
			}
			s.setTaskStageOn(ctx, todo.ID, "review", ownDeliveryPR, "", false, "")
			before := requirePersistedDefaultDelivery(t, s, todo.ID, []string{ownDeliveryPR})
			forge.ownedPRs = map[string]bool{"o/r#21": false}
			artifactReportProof(s.hub.(*fakeHub), todo, researchNoPRCloseout)
			if _, err := acceptReview(s, todo.ID, nil); err == nil || !strings.Contains(err.Error(), "not merged yet") {
				t.Fatalf("saved default-role delivery did not hold acceptance: %v", err)
			}
			got := todoNow(s, todo.ID)
			if got.DoneAt != nil || got.PRURL != ownDeliveryPR {
				t.Fatalf("no-PR closeout erased saved delivery state: %+v", got)
			}
			if after := requirePersistedDefaultDelivery(t, s, todo.ID, []string{ownDeliveryPR}); after != before {
				t.Fatalf("no-PR closeout rewrote the saved binding: revision %d -> %d", before, after)
			}
			restarted, _ := newTaskServiceOn(t, s.store())
			if got := restarted.todoStages(ctx)[todo.ID]; got.PR != ownDeliveryPR {
				t.Fatalf("saved delivery stage changed across restart: %+v", got)
			}
			requirePersistedDefaultDelivery(t, restarted, todo.ID, []string{ownDeliveryPR})
		})
	}
}

// Explicit multi-repo bindings must all survive even when the last report says
// no PR and both authors differ from the currently connected forge viewer.
func TestExplicitDefaultMultiRepoDeliverySurvivesNoPRCloseout(t *testing.T) {
	s, todo, forge := linkRoleTask(t)
	const secondPR = "https://github.com/o/client/pull/32"
	urls := []string{ownDeliveryPR, secondPR}
	if err := s.importTodoRepositoryLinks(t.Context(), todo.ID, urls, "explicit"); err != nil {
		t.Fatal(err)
	}
	before := requirePersistedDefaultDelivery(t, s, todo.ID, urls)
	s.deploy.forge = multiRepoDeliveryForge{forge} // first merged, second unmerged
	forge.ownedPRs = map[string]bool{"o/r#21": false, "o/client#32": false}
	artifactReportProof(s.hub.(*fakeHub), todo, researchNoPRCloseout)
	if _, err := acceptReview(s, todo.ID, nil); err == nil || !strings.Contains(err.Error(), secondPR) {
		t.Fatalf("no-PR report bypassed the second saved repository gate: %v", err)
	}
	if todoNow(s, todo.ID).DoneAt != nil {
		t.Fatal("task closed with an unmerged second delivery")
	}
	if after := requirePersistedDefaultDelivery(t, s, todo.ID, urls); after != before {
		t.Fatalf("no-PR report rewrote explicit deliveries: revision %d -> %d", before, after)
	}
}

func requirePersistedDefaultDelivery(t *testing.T, s *Service, id string, want []string) int64 {
	t.Helper()
	set, err := s.savedTodoRepositories(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, row := range set.Repositories {
		for _, link := range row.Links {
			if link.Kind != "pull_request" || link.Role != "" {
				t.Fatalf("expected original empty-role PR binding, got %+v", link)
			}
			urls = append(urls, link.CanonicalURL)
			if role := linkRoles(t, s, id)[link.CanonicalURL]; role != repository.LinkRoleDelivery {
				t.Fatalf("default role lost delivery semantics: %q", role)
			}
		}
	}
	slices.Sort(urls)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(urls, want) {
		t.Fatalf("saved delivery URLs = %v, want %v", urls, want)
	}
	return set.Revision
}
