package assistant

import (
	"context"
	"errors"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ultracodeRefusal: a session.start that asked for a launch workflow was refused for it. The selected executable,
// terminal, or runtime may not support it (ultracode_unsupported), or the node may be too old (node_outdated). The
// caller starts the agent again without it and says why (#3596): the apps' New Agent does the same after showing the
// refusal. Both refusals come before anything starts, so the second start is the only agent. reason is the node's own
// words (it has them in Chinese only), the hub's in the user's language for an old node.
func (s *Service) ultracodeRefusal(ctx context.Context, err error, params map[string]any) (reason string, refused bool) {
	if on, _ := params["ultracode"].(bool); !on {
		return "", false
	}
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != "ultracode_unsupported" && re.Code != proto.CodeNodeOutdated {
		return "", false
	}
	if re.Code == proto.CodeNodeOutdated {
		return i18n.T(s.lang(ctx), "hub-ultracode.node_outdated"), true // worded where no language was at hand
	}
	return re.Message, true
}
