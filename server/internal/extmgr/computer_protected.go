package extmgr

// Apps the assistant never operates (#4336, docs/extensions/computer-use.md "c) Per-App approval"): System Settings
// and the system's permission and authorization prompts. macOS's privacy permissions are meant to be answered by a
// person, and one Allow on a card that says "operate System Settings" would otherwise let an Agent turn any of them on.
// A call that names one of these apps is refused before any card. So is a script whose apps cannot all be read (it
// could open one by a computed name), and, on Cua Driver, any call while one of them has a window on the screen:
// element tokens, a pid or a step naming no app could still reach it. Steps naming no app run one at a time, each after
// the same look at the screen, so a prompt an earlier step brought up stops the rest. Reading the screen
// (computer_state) stays as it was: asked every time. Scripts are only looked at before they run; what one does
// with a window it finds while running is Cua Driver's (and the follow-up issue's) to stop.

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// protectedIDs: System Settings (both ids it has had) and the processes that show macOS's permission and
// authorization prompts: the privacy alerts, administrator sheets, Touch ID / password prompts, and the
// Accessibility alert of older systems.
var protectedIDs = map[string]bool{
	"com.apple.systempreferences": true, "com.apple.settings": true,
	"com.apple.usernotificationcenter": true, "com.apple.securityagent": true,
	"com.apple.localauthentication.uiagent": true, "com.apple.accessibility.universalaccessauthwarn": true,
}

// protectedNames: the same by the name a call or a window gives (case aside), in the languages the app ships plus
// the prompts' process names, for apps Cua Driver's list does not carry (the prompts run only while they show).
var protectedNames = []string{
	"system settings", "system preferences",
	// i18n-ignore: the names macOS itself gives System Settings, matched against what a call or a window says
	"系统设置", "系統設定", "システム設定", "系统偏好设置", "システム環境設定",
	"usernotificationcenter", "securityagent", "coreautha", "universalaccessauthwarn",
}

// protectedApp: a is one of the apps never operated, by bundle id, or by name when its bundle id is unknown (a name
// that is a bundle id counts as one).
func protectedApp(a approvedApp) bool {
	name := strings.ToLower(strings.TrimSpace(a.Name))
	return protectedIDs[strings.ToLower(a.BundleID)] || protectedIDs[name] || slices.Contains(protectedNames, name)
}

// refusedText is what the model is told; it passes the gist on to the user.
func refusedText(machine, why string) string {
	return fmt.Sprintf("Refused on %s: %s. Persona never operates System Settings or macOS's permission and password "+
		"prompts for anyone. Tell the user to do this themselves, on the Mac or through its live screen in the Persona app. "+
		"Do not retry it or look for another way.", machine, why)
}

// knownApp is one of Cua Driver's list_apps: bundle id, name, and pid while running.
type knownApp struct {
	Name     string `json:"name"`
	BundleID string `json:"bundle_id"`
	PID      int    `json:"pid"`
	Running  bool   `json:"running"`
}

// knownApps is Cua Driver's list of m's apps, nil when it cannot be read.
func (h *Manager) knownApps(ctx context.Context, m *Machine) []knownApp {
	var out struct {
		Apps []knownApp `json:"apps"`
	}
	if res, err := h.computerCall(ctx, m, "persona-apps", "", "list_apps", map[string]any{}); err == nil {
		_ = json.Unmarshal(res.Output, &out)
	}
	return out.Apps
}

// protectedOnScreen is the protected apps with a window on m's screen now, by name. ok is false when the screen
// could not be read: the caller refuses then, rather than act blind. known is refreshed when a window's pid is not in it
// (an app started since).
func (h *Manager) protectedOnScreen(ctx context.Context, m *Machine, known *[]knownApp) (found []string, ok bool) {
	res, err := h.computerCall(ctx, m, "persona-apps", "", "list_windows", map[string]any{})
	if err != nil {
		return nil, false
	}
	var out struct {
		Windows *[]struct {
			AppName  string `json:"app_name"`
			PID      int    `json:"pid"`
			OnScreen bool   `json:"is_on_screen"`
		} `json:"windows"`
	}
	if json.Unmarshal(res.Output, &out) != nil || out.Windows == nil {
		return nil, false
	}
	byPID := func() map[int]knownApp {
		ids := map[int]knownApp{}
		for _, a := range *known {
			if a.PID > 0 {
				ids[a.PID] = a
			}
		}
		return ids
	}
	ids, refreshed := byPID(), false
	for _, w := range *out.Windows {
		if !w.OnScreen {
			continue
		}
		app, has := ids[w.PID]
		if !has && !refreshed && w.PID > 0 {
			*known, refreshed = h.knownApps(ctx, m), true
			ids = byPID()
			app, has = ids[w.PID]
		}
		a := approvedApp{BundleID: app.BundleID, Name: w.AppName}
		if has && app.Name != "" {
			a.Name = app.Name
		}
		if protectedApp(a) || protectedApp(approvedApp{Name: w.AppName}) {
			if name := cmp.Or(a.Name, a.BundleID); !slices.Contains(found, name) {
				found = append(found, name)
			}
		}
	}
	return found, true
}

// unnamedSteps: some step names no app of its own and the call names none either (it acts on whatever has the focus,
// or where it points).
func unnamedSteps(app string, steps json.RawMessage) bool {
	if strings.TrimSpace(app) != "" {
		return false
	}
	var list []map[string]json.RawMessage
	_ = json.Unmarshal(steps, &list)
	for _, s := range list {
		if opened, ok := launchTarget(s); ok {
			if opened == "" {
				return true
			}
			continue
		}
		named := false
		for k, raw := range s {
			if k == "wait_for" || k == "expect" || k == "tool" {
				continue
			}
			var v struct {
				App string `json:"app"`
			}
			if json.Unmarshal(raw, &v) == nil && strings.TrimSpace(v.App) != "" {
				named = true
			}
		}
		if !named {
			return true
		}
	}
	return false
}

// runStepsOneByOne runs steps one run_steps call at a time, looking at the screen before each after the first (which
// the caller looked at): a protected app's window stops the rest. The answer lists each step's own answer; the last
// step carries the call's observe, and its screenshot is the answer's.
func (h *Manager) runStepsOneByOne(ctx context.Context, m *Machine, session, conversation string, steps, observe json.RawMessage,
	known *[]knownApp) (*proto.ComputerResult, string, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(steps, &list); err != nil {
		return nil, "", err
	}
	outs := make([]json.RawMessage, 0, len(list))
	var last *proto.ComputerResult
	for i, step := range list {
		if i > 0 {
			found, ok := h.protectedOnScreen(ctx, m, known)
			if !ok {
				return nil, "", fmt.Errorf("stopped before step %d of %d (the ones before it ran): the Mac's windows could not "+
					"be read to check for System Settings or a permission prompt", i+1, len(list))
			}
			if len(found) > 0 {
				return nil, fmt.Sprintf("%s came up on the screen before step %d (%d of %d done)", strings.Join(found, ", "), i+1, i, len(list)), nil
			}
		}
		in := map[string]any{"steps": []json.RawMessage{step}}
		if i == len(list)-1 && len(observe) > 0 {
			in["observe"] = observe
		}
		res, err := h.computerCall(ctx, m, session, conversation, "run_steps", in)
		if err != nil {
			return nil, "", err
		}
		last = res
		out := res.Output
		if len(out) == 0 {
			out, _ = json.Marshal(res.Text)
		}
		outs = append(outs, out)
		var r struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(res.Output, &r) == nil && r.OK != nil && !*r.OK {
			break // stopped at its first failure, as run_steps does
		}
	}
	all, _ := json.Marshal(map[string]any{"one_by_one": true, "steps": outs})
	return &proto.ComputerResult{Output: all, Image: last.Image}, "", nil
}
