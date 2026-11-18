package assistant

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A task whose agent did not start because of its launcher on that computer (the terminal cannot run it, its key or
// model route is broken, its subscription is used up) starts again on another launcher of the same computer only when
// someone says so (#10365): task_retry's launcher, or the app's "Use <launcher> instead" (POST /api/v1/todos/{id}/retry
// with {launcher, model}, hello feature task_retry_launcher). The hub never swaps a named launcher by itself; it
// suggests (PlanTask.RetryWith) and checks the one asked for before anything is reopened. The task keeps its computer,
// directory and workspace (the checkout it already made is used again), so the retry is the same task on another
// launcher, not a new one.

// retryWith is another launcher (and its model) a retry starts a task's agent with; zero: the one its plan has. by: who
// asked (user), for the start_remedy step that records the change; "" when the caller writes its own (task_retry).
type retryWith struct {
	launcher, model, by string
}

// retryRefused: a retry on another launcher refused before anything changed (409, the words say why).
type retryRefused struct{ error }

func (e retryRefused) Unwrap() error { return e.error }

// retryWithMax bounds the launchers a failed task suggests.
const retryWithMax = 3

// launcherSpecificFailure: err is a failed start that another launcher on the same computer may not have: the
// terminal could not run the launcher (launcher_missing), the computer does not have the one the plan named, its health
// record kept it off a fixed computer, or its error reads as its key, its model route or its subscription's limit. A
// computer that is offline or a link that dropped is not the launcher's, nor is Claude Code missing (every Claude Code
// launcher needs it), nor the request's own refusal.
func launcherSpecificFailure(err error) bool {
	if err == nil || transientStartErr(err) || errors.Is(err, errMachineOffline) || startRefused(err) || startStalled(err) {
		return false
	}
	var re *rpc.Error
	if errors.As(err, &re) && re.Code == proto.CodeLauncherMissing {
		return true
	}
	var m *i18n.Message
	if errors.As(err, &m) && (m.Key == "assistant-tasks.fixed_launcher_unhealthy" || m.Key == "assistant-tasks.launcher_not_on_machine") {
		return true
	}
	switch classifyLauncherError(err.Error()) {
	case errKindAuth, errKindModelRoute, errKindUsageLimit:
		return true
	}
	return false
}

// personadMissing: t's start failed because the terminal could not run the file a Persona-managed launcher starts
// through (personad itself: launcher_missing, where terminal, a path it could not run, node/shellmiss.go). Every
// managed launcher starts through the same program, so none of them is a way out until that is fixed.
func (s *Service) personadMissing(ctx context.Context, t PlanTask) bool {
	a := t.ErrorArgs
	if t.ErrorCode != proto.CodeLauncherMissing || a["where"] != "terminal" || a["kind"] != "no_such_file" && a["kind"] != "permission_denied" {
		return false
	}
	return s.managedLauncher(ctx, cmp.Or(a["launcher"], t.Launcher))
}

// managedLauncher: name is a Persona-managed launcher (Settings › Models), which a node runs through personad launch.
func (s *Service) managedLauncher(ctx context.Context, name string) bool {
	return name != "" && slices.ContainsFunc(s.storedLaunchers(ctx), func(l storedLauncher) bool { return l.Name == name })
}

// retryLaunchers are the launchers task t, whose start on computer m just failed with err, may start again with
// there (launcherAlternatives), suggested only for a failure of its launcher (launcherSpecificFailure); t carries the
// failure's code and args.
func (s *Service) retryLaunchers(ctx context.Context, m Device, t PlanTask, err error) []string {
	if !launcherSpecificFailure(err) {
		return nil
	}
	return s.launcherAlternatives(ctx, m, t)
}

// launcherAlternatives are the launchers other than t's (and except) that t may start with on m: plain claude first
// when m has Claude Code (launcherInstead's order), then m's own and the managed ones in their list's order, each
// running the agent t's launcher runs, never one the health records say cannot run there now, or a managed one while
// the terminal could not run personad itself (personadMissing). A list that can't be read gives plain claude alone, when
// m has it and t's launcher runs Claude Code.
func (s *Service) launcherAlternatives(ctx context.Context, m Device, t PlanTask, except ...string) []string {
	failed := s.startLauncher(ctx, t.Launcher)
	list, known := s.launchersHere(withUnhidden(ctx, failed), m.ID)
	var configured []ConfiguredLauncher
	if known {
		for _, l := range list {
			configured = append(configured, ConfiguredLauncher{Launcher: l})
		}
		configured = sessionLaunchers(configured, s.hello(ctx).Models.Launchers[m.ID])
	}
	kind := "claude"
	if i := slices.IndexFunc(configured, func(l ConfiguredLauncher) bool { return l.Name == failed }); i >= 0 {
		kind = cmp.Or(configured[i].AgentType, kind)
	} else {
		for _, l := range s.storedLaunchers(ctx) {
			if l.Name == failed {
				kind = cmp.Or(l.AgentType(), kind)
			}
		}
	}
	noManaged := s.personadMissing(ctx, t)
	var out []string
	add := func(name, agent string, managed bool) {
		if len(out) >= retryWithMax || name == "" || name == failed || slices.Contains(except, name) || slices.Contains(out, name) ||
			cmp.Or(agent, "claude") != kind || managed && noManaged {
			return
		}
		if _, bad := s.launcherUnusable(ctx, m.ID, name); bad {
			return
		}
		out = append(out, name)
	}
	if m.ClaudeVersion != "" || slices.ContainsFunc(list, func(l proto.Launcher) bool { return l.Name == "claude" }) {
		add("claude", "claude", false)
	}
	for _, l := range configured {
		if startableLauncher(l.Launcher) {
			add(l.Name, l.AgentType, compat.Source(l.Source) == "persona")
		}
	}
	return out
}

// retryOn makes task t, whose start failed on computer m, start again with another launcher (with.launcher, and
// with.model or that launcher's default), checked before anything changes: the launcher runs on m (fitLauncher: one
// m's list lacks is refused with the ones it has), is not one a health record says cannot run there now (a terminal
// record excepted: only a real start clears it, as an explicit retry on a fixed computer goes back to it), holds a
// read-only task read-only, and is not a managed one while the terminal could not run personad itself. t keeps its
// computer, directory and workspace; its tier goes (it named the launcher before), the permission is settled again
// for the agent that runs, and its why says what changed. The same launcher it has, or none, changes nothing.
func (s *Service) retryOn(ctx context.Context, t *PlanTask, m Device, with retryWith) error {
	name, model := strings.TrimSpace(with.launcher), strings.TrimSpace(with.model)
	if name == "" {
		if model != "" {
			return planInvalid{i18n.Err("assistant-tasks.retry_model_without_launcher")}
		}
		return nil
	}
	if err := checkModelID(model); err != nil {
		return planInvalid{err}
	}
	from := s.startLauncher(ctx, t.Launcher)
	if name == from && model == "" {
		return nil
	}
	machine := firstNonEmpty(m.Name, m.ID)
	alternatives := func() string {
		return cmp.Or(strings.Join(s.launcherAlternatives(ctx, m, *t, name), ", "), "—")
	}
	if name != from && s.managedLauncher(ctx, name) && s.personadMissing(ctx, *t) {
		return retryRefused{i18n.Err("assistant-tasks.retry_launcher_managed_missing", i18n.Args{
			"launcher": name, "missing": t.ErrorArgs["missing"], "machine": machine, "launchers": alternatives()})}
	}
	params := map[string]any{"launcher": name}
	if model != "" {
		params["model"] = model
	}
	_, agent, err := s.fitLauncher(ctx, m, startPick{launcher: name, model: model}, params)
	if err != nil {
		return retryRefused{err}
	}
	if r, bad := s.launcherUnusableNow(ctx, m.ID, name); bad && r.Kind != errKindNotInTerminal && name != from {
		return retryRefused{i18n.Err("assistant-tasks.retry_launcher_unusable", i18n.Args{
			"launcher": name, "machine": machine, "reason": r.Error, "launchers": alternatives()})}
	}
	if t.Readonly {
		if err := s.readonlyLauncherRefusal(ctx, m, params, agent); err != nil {
			return retryRefused{err}
		}
	}
	t.Launcher, t.Model, t.Tier = name, model, ""
	modes := startTakesModes(agent.agent)
	if !modes {
		t.Effort = ""
	}
	if !t.preservePermission {
		// Settled again for the agent that runs now, as task_plan settles it: what the last launcher ran with (Allow
		// all for Claude Code) is not carried onto another kind.
		t.Permission = ""
		t.normalizeReadonlyPermission(readonlyModeOf(s.settings(ctx)))
		if agent.allowAll() {
			t.Permission = "bypassPermissions"
		}
		if t.Permission == "" && modes {
			t.Permission = s.taskPermission(ctx, t.Machine, nil)
		}
	}
	t.RetryWith = nil
	t.noteWhy(i18n.T(s.lang(ctx), "assistant-tasks.retried_on_launcher", i18n.Args{"to": name, "from": from, "machine": machine}))
	s.logf("task %s: retrying its start on %s with launcher %s instead of %s", t.TodoID, m.ID, name, from)
	return nil
}
