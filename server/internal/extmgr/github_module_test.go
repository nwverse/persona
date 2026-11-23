package extmgr

import (
	"testing"

	"github.com/nwverse/persona/server/internal/extmgr/githubport"
	"github.com/nwverse/persona/server/internal/githubwire"
)

// needsGitHubModule is the GitHub module's factory, as the manager gets it (githubport, filled by internal/modules/all,
// which modules_link_test.go links); the test is skipped in a build without the module.
func needsGitHubModule(t *testing.T) githubwire.Factory {
	t.Helper()
	f := githubport.Factory()
	if f == nil {
		t.Skip("this build has no GitHub module (persona_without_github or persona_core_only)")
	}
	return f
}

// newTestGitHub is the module's GitHub connector for cfg, reading machine's token through fetch (nil: the device flow).
func newTestGitHub(t *testing.T, cfg githubwire.Config, machine string, fetch githubwire.TokenSource) githubwire.Connector {
	t.Helper()
	return needsGitHubModule(t)(cfg, machine, fetch)
}
