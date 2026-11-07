package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// healthReportCall asks the machine about the health space's reports (docs/assistant/health.md"Health reports"), this manager's
// (another than the built-in names its own space, health-<id>); a node that predates them, or other managers' health
// spaces (nodeproto.HealthSpaces: it would answer about the built-in's), says so in words the app can show.
func (d *Devices) healthReportCall(w http.ResponseWriter, r *http.Request, m *Machine, method string, params map[string]any) {
	outdated := func() {
		msg := i18n.T(i18n.FromRequest(r), "hub-health.reports_outdated", i18n.Args{"name": m.name()})
		writeErrCode(w, http.StatusConflict, proto.CodeNodeOutdated, errors.New(msg))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// call asks the node; ok false once it answered the request (an error, or a node that did not say it used the
	// manager's own space: it would have used the built-in's).
	space := ""
	call := func(method string, params map[string]any) (json.RawMessage, bool) {
		var out json.RawMessage
		if err := m.call(ctx, method, params, &out); err != nil {
			var re *rpc.Error
			if errors.As(err, &re) && strings.HasPrefix(re.Message, "unknown method") {
				outdated()
				return nil, false
			}
			apierr.Call(w, err)
			return nil, false
		}
		var echo struct {
			HealthDir string `json:"health_dir"`
		}
		if _ = json.Unmarshal(out, &echo); echo.HealthDir != space {
			outdated()
			return nil, false
		}
		return out, true
	}
	if name := identity.HealthDirName(d.opts.ManagerID); name != identity.SpaceHealth {
		m.mu.Lock()
		info := m.info
		m.mu.Unlock()
		if !nodeHas(info, nodeproto.HealthSpaces) {
			outdated()
			return
		}
		space, params["health_dir"] = name, name
		// A delete goes only to a node that has just said it reads the manager's own space (a list of it, echoed):
		// one that ignored health_dir would delete in the built-in's.
		if method == "health.reports.delete" {
			if _, ok := call("health.reports.list", map[string]any{"health_dir": name}); !ok {
				return
			}
		}
	}
	out, ok := call(method, params)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}

// listHealthReports: GET /api/v1/machines/{mid}/health/reports.
func (d *Devices) listHealthReports(w http.ResponseWriter, r *http.Request, m *Machine) {
	d.healthReportCall(w, r, m, "health.reports.list", map[string]any{})
}

// deleteHealthReport: DELETE /api/v1/machines/{mid}/health/reports/{name}.
func (d *Devices) deleteHealthReport(w http.ResponseWriter, r *http.Request, m *Machine) {
	d.healthReportCall(w, r, m, "health.reports.delete", map[string]any{"name": r.PathValue("name")})
}
