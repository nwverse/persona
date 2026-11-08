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

// Renaming, moving and trashing files from the Files browser (docs/protocol/protocol.md "Changing files"):
// POST /api/v1/machines/{mid}/files/{rename,move,trash,restore} with a JSON body, passed to the node's file.<op> and
// its answer returned as it is. The node checks what may be changed (read_only, exists); the hub answers 400 for a
// request that can't be one, 404 not_found for a file that is not there and 409 node_outdated for a node from before.

const maxFileEditPaths = 1000

type fileEditRequest struct {
	Path     string   `json:"path,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Cwd      string   `json:"cwd,omitempty"`
	Name     string   `json:"name,omitempty"`
	Dir      string   `json:"dir,omitempty"`
	Conflict string   `json:"conflict,omitempty"`
	TrashIDs []string `json:"trash_ids,omitempty"`
}

// check says what makes the request not one of op's, nil when it is one.
func (q fileEditRequest) check(op string) error {
	nonEmpty := func(list []string, what string) error {
		if len(list) == 0 || len(list) > maxFileEditPaths {
			return errors.New(what + " must list 1 to 1000")
		}
		for _, p := range list {
			if strings.TrimSpace(p) == "" {
				return errors.New(what + " can't have an empty one")
			}
		}
		return nil
	}
	switch op {
	case "rename":
		if strings.TrimSpace(q.Path) == "" {
			return errors.New("path is required")
		}
		if n := q.Name; strings.TrimSpace(n) == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\x00") {
			return errors.New("name can't be a file's name")
		}
	case "move":
		if strings.TrimSpace(q.Dir) == "" {
			return errors.New("dir is required")
		}
		switch q.Conflict {
		case "", proto.UploadConflictReplace, proto.UploadConflictKeep:
		default:
			return errors.New("conflict must be replace or keep")
		}
		return nonEmpty(q.Paths, "paths")
	case "trash":
		return nonEmpty(q.Paths, "paths")
	case "restore":
		return nonEmpty(q.TrashIDs, "trash_ids")
	}
	return nil
}

func (h *Hub) fileEdit(op string) http.HandlerFunc {
	return h.withMachine(func(w http.ResponseWriter, r *http.Request, m *Machine) {
		var q fileEditRequest
		if err := decodeJSONBody(w, r, &q, 1<<20); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if err := q.check(op); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute) // a big folder to the trash on a slow disk
		defer cancel()
		var out json.RawMessage
		if err := m.call(ctx, "file."+op, q, &out); err != nil {
			var re *rpc.Error
			switch {
			case errors.As(err, &re) && re.Code == "" && strings.HasPrefix(re.Message, "unknown method"):
				writeCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.T(h.requestLang(r), "hub-fileedit.outdated", i18n.Args{"name": m.cfg.Name}), nil)
			case errors.As(err, &re) && re.Code == proto.CodeNotFound:
				writeCode(w, http.StatusNotFound, proto.CodeNotFound, re.Message, nil)
			default:
				callErr(w, err)
			}
			return
		}
		h.logf("[%s] files %s from the phone", m.cfg.ID, op)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(out)
	})
}
