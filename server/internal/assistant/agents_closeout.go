package assistant

import (
	"context"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A closeout is what an agent says of its own work as a turn ends (docs/assistant/agent-work.md "Closeout"): done or
// not, what changed, merged and deployed or not, what was verified, what is missing. The node holds a stop once to
// get one (node/closeout_stop.go) when the manager says so here; the triage reads it before it asks a model.

// closeoutCheck answers a node whose agent is about to stop, after a turn that did work, without a closeout: the
// reason to hold the stop with, or none. Only under the triage's main switch and its closeout switch, and never for
// an assistant conversation or a review's reviewer.
func (s *Service) closeoutCheck(ctx context.Context, machine string, p assistantwire.CloseoutCheckRequest) assistantwire.CloseoutCheckResponse {
	var out assistantwire.CloseoutCheckResponse
	if sw := s.triageSwitches(ctx); !sw.Enabled || !sw.Closeout {
		return out
	}
	if p.SessionID == "" || s.isReviewer(machine, p.SessionID) {
		return out
	}
	if c, ok := s.session(ctx, machine, p.SessionID); ok && c.Assistant {
		return out
	}
	out.Reason = renderCloseoutRequired(answerLanguage(s.hello(ctx).Locale))
	s.workEvent(machine, p.SessionID, "closeout_asked", nil)
	return out
}

func renderCloseoutRequired(language string) string {
	return prompts.RenderAgentCloseoutRequired(prompts.AgentCloseoutRequired{Form: closeout.Example, Language: language})
}

// closeoutDone is the work state of a turn whose closeout says done: no model is asked. The line is the first thing
// it says it did.
func closeoutDone(c proto.Closeout, artifacts []proto.WorkArtifact, lang i18n.Lang) *proto.WorkState {
	w := &proto.WorkState{Phase: proto.WorkDone, Closeout: &c, Artifacts: workArtifacts(artifacts), By: "rule"}
	if len(c.Did) > 0 {
		w.Line = clip(oneLine(c.Did[0]), 60)
	}
	if w.Line == "" {
		w.Line = i18n.T(lang, "assistant-agents_triage.done")
	}
	return w
}

// withCloseout lays a closeout that is not "done" over what the model read from the same reply. The agent's own word
// that it is not finished wins over a model that read the reply as done: the state is then unsure, as for any turn
// nobody can call finished, with the closeout kept for whoever looks.
func withCloseout(w *proto.WorkState, c proto.Closeout, artifacts []proto.WorkArtifact) *proto.WorkState {
	if w.Phase == proto.WorkDone {
		w = &proto.WorkState{Artifacts: w.Artifacts}
	}
	w.Closeout = &c
	if len(w.Artifacts) == 0 {
		w.Artifacts = workArtifacts(artifacts)
	}
	return w
}

// workArtifacts keeps the kinds a work state lists (a closeout may also name a branch).
func workArtifacts(in []proto.WorkArtifact) []proto.WorkArtifact {
	var out []proto.WorkArtifact
	for _, a := range in {
		if strings.TrimSpace(a.Ref) != "" {
			out = append(out, a)
		}
	}
	return out
}
