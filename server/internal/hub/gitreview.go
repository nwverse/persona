package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// gitReview relays GET /api/v1/machines/{mid}/git/review?cwd=&scope=&base=&path=… to the node's git.review: every
// file a scope changed, with its hunks, for the web's Review panel (docs/web/review.md, #151). Read like git/diff:
// full credentials only. The node's refusals (not_git, bad_base, no_default_branch…) are 409 with their code; a node
// from before it 409 node_outdated.
func (h *Hub) gitReview(w http.ResponseWriter, r *http.Request, m *Machine) {
	q := r.URL.Query()
	p := proto.GitReviewRequest{Cwd: q.Get("cwd"), Scope: q.Get("scope"), Base: q.Get("base"), Paths: q["path"]}
	if strings.TrimSpace(p.Cwd) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("cwd is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	var out json.RawMessage
	if err := m.call(ctx, "git.review", p, &out); err != nil {
		var re *rpc.Error
		if errors.As(err, &re) && strings.HasPrefix(re.Message, "unknown method") {
			writeCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.T(h.requestLang(r), "hub-gitreview.outdated"), nil)
			return
		}
		callErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}
