package assistant

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/repositorystore"
)

// stageOnlyDeliveryReviewService deliberately creates no saved PR binding. Recovery tests
// seed legacy stage facts separately; saved delivery bindings have their own gate tests.
func stageOnlyDeliveryReviewService(t *testing.T, hub *runningHub) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f, todo := newSupervisedServiceOn(t, repositorystore.New(todoTestDB(t)))
	s.deploy.forge = &fakeForge{}
	if hub != nil {
		s.deploy.hubBuild = hub.build
	} else {
		s.deploy.hubBuild = func(context.Context) (string, string, error) { return "dev", "", nil }
	}
	s.deploy.listPaths = []string{}
	deliveryOn(t, s)
	requireStageOnlyRecoveryLinks(t, s, todo.ID)
	return s, f, todo
}

func requireStageOnlyRecoveryLinks(t *testing.T, s *Service, todoID string) {
	t.Helper()
	set, err := s.savedTodoRepositories(t.Context(), todoID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range set.Repositories {
		if len(row.Links) != 0 {
			t.Fatalf("stage-only recovery fixture unexpectedly saved bindings: %+v", set)
		}
	}
}
