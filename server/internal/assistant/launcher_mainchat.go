package assistant

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- which launchers can run the assistant's conversations ----
//
// The main chat, a scheduled task's conversation and the side conversations run headless on their machine: claude -p
// or codex app-server started through the launcher's shell command (node/assistant_start.go), or a runtime that
// declared caps.assistant. The picker (?purpose=assistant) lists assistantLaunchers (modelProviderCompatible), and the
// check before each message (sendModel) holds a launcher to the same list through mainChatFit. On top of that, the
// pickers leave out a launcher whose latest health check failed (launcher_health_check.go, withHealth): health keeps a
// message from being refused as launcher_missing on a wrong read, but a failing launcher is not refused here, and a
// model configuration that names one is left alone.

// mainChatFit is whether launcher name can run an assistant conversation on machine: in the main chat's list (fit),
// one the machine has that can't run it (refused, with why in the user's language), or not there at all (gone).
// Plain claude fits without asking; a list that can't be read fits too, as launchersHere has it: nothing is refused
// or replaced on a guess.
type mainChatFit int

const (
	mainChatFits mainChatFit = iota
	mainChatRefused
	mainChatGone
)

func (s *Service) mainChatFit(ctx context.Context, machine, name string) (mainChatFit, error) {
	if name == "" || name == "claude" {
		return mainChatFits, nil
	}
	ctx = withSettingsMemo(ctx)
	if m, managed := s.managedNamed(ctx, name); managed { // the same on every machine in its scope: no list to ask
		allowed, err := s.assistantProviders(ctx, machine, nil)
		switch {
		case !s.usableOn(ctx, m, machine):
			return mainChatGone, nil
		case err != nil || allowed[name]:
			return mainChatFits, nil
		}
		return mainChatRefused, s.mainChatRefusal(ctx, machine, name)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	picker, err := s.pickerLaunchers(cctx, machine)
	if err != nil || !slices.ContainsFunc(picker, func(l ConfiguredLauncher) bool { return l.Source != "" && compat.Source(l.Source) != "persona" }) {
		return mainChatFits, nil // the machine's own list did not come: a shell launcher may still be there
	}
	fits, err := s.assistantLaunchersOf(cctx, machine, picker)
	named := func(l ConfiguredLauncher) bool { return l.Name == name }
	switch {
	case err != nil || slices.ContainsFunc(fits, named):
		return mainChatFits, nil
	case slices.ContainsFunc(picker, named):
		return mainChatRefused, s.mainChatRefusal(ctx, machine, name)
	}
	return mainChatGone, nil
}

// finishMainChatModel validates the binding after model resolution, including a default or a legacy fallback that
// was not the caller's explicit pick. A bad default gives way to a launcher from the current compatible picker;
// plain claude is preferred only when that list verified it. With no verified compatible launcher, nothing is sent.
func (s *Service) finishMainChatModel(ctx context.Context, machine, purpose string, params map[string]any, picked bool) error {
	name, _ := params["launcher"].(string)
	fit, refusal := s.mainChatFit(ctx, machine, name)
	if fit == mainChatFits {
		return nil
	}
	launchers, err := s.assistantLaunchers(ctx, machine)
	if err != nil {
		return err
	}
	fallback := ""
	for _, l := range launchers {
		if l.Name == "claude" {
			fallback = l.Name
			break
		}
		if fallback == "" {
			fallback = l.Name
		}
	}
	if fallback == "" {
		if refusal != nil {
			return refusal
		}
		return s.mainChatRefusal(ctx, machine, name)
	}
	s.logf("[%s] resolved assistant launcher %s can't run it, using %s", machine, name, fallback)
	params["launcher"] = fallback
	delete(params, "model")
	if picked {
		return s.applyPickedModel(ctx, params)
	}
	return s.applyModel(ctx, purpose, params)
}

// mainChatRefusal says that launcher can't run the assistant on machine, for the user's language.
func (s *Service) mainChatRefusal(ctx context.Context, machine, launcher string) error {
	d, _ := s.device(ctx, machine)
	return i18n.Err("assistant-start.launcher_not_main_chat", i18n.Args{"launcher": launcher, "machine": firstNonEmpty(d.Name, machine)})
}

// ---- what can't run the assistant's conversations, and why ----

// Why a launcher or provider is in the main chat picker's unavailable list (GET /api/v1/machines/{mid}/launchers?purpose=
// assistant, #5033), besides proto.CodeLauncherOutOfScope and proto.CodeLauncherOwnService.
const (
	unavailableAPIProtocol = "api_protocol"  // a direct API provider speaking Responses or Chat Completions, not Messages
	unavailableNotMainChat = "not_main_chat" // its agent or engine can't run the assistant (a Paseo provider, managed Codex…)
)

// unavailableLauncher is one launcher or provider the main chat can't run on a machine, and why in the user's words.
type unavailableLauncher struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"` // a Persona-managed provider's own name ("" shows Name)
	Kind        string `json:"kind"`                   // launcher | api
	Agent       string `json:"agent,omitempty"`
	API         string `json:"api,omitempty"` // a direct API provider's protocol: messages | responses | chat
	Code        string `json:"code"`
	Reason      string `json:"reason"`
}

// mainChatUnavailable lists what the main chat's model panel shows as unavailable on machine, with why in lang: the
// machine's launchers (picker, pickerLaunchers) that can't run the assistant (fits, assistantLaunchersOf, are the ones
// that can), then Settings › Model providers' providers that can't run it here: a direct API that speaks Responses or
// Chat Completions, one whose agent can't, one out of the computer's device scope or skipping a computer with its
// own sign-in. Hidden launchers stay out, and so does a provider that would run here but is not listed for another
// reason (a failed health check, a list that has not come yet): nothing here refuses it.
func (s *Service) mainChatUnavailable(ctx context.Context, machine string, picker, fits []ConfiguredLauncher, lang i18n.Lang) []unavailableLauncher {
	ctx = withSettingsMemo(ctx)
	out := []unavailableLauncher{}
	seen := map[string]bool{}
	for _, l := range fits {
		seen[l.Name] = true
	}
	for _, name := range s.hiddenLaunchers(ctx) {
		seen[name] = true
	}
	stored := s.storedLaunchers(ctx)
	managed := func(name string) (storedLauncher, bool) {
		i := slices.IndexFunc(stored, func(l storedLauncher) bool { return l.Name == name })
		if i < 0 {
			return storedLauncher{}, false
		}
		return stored[i], true
	}
	d, _ := s.device(ctx, machine)
	notMainChat := i18n.T(lang, "assistant-model_pick.unavailable_not_main_chat", i18n.Args{"machine": firstNonEmpty(d.Name, machine)})
	for _, l := range picker {
		if seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		u := unavailableLauncher{Name: l.Name, Kind: "launcher", Agent: cmp.Or(l.AgentType, l.Agent), Code: unavailableNotMainChat, Reason: notMainChat}
		if m, ok := managed(l.Name); ok {
			u.DisplayName = m.DisplayName
		}
		out = append(out, u)
	}
	p, err := s.modelPurposeDef(ctx, "assistant")
	p.MachineID = "" // what the provider is, its scope apart: that is the next reason
	for _, l := range stored {
		if seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		kind := cmp.Or(l.Kind, "launcher")
		u := unavailableLauncher{Name: l.Name, DisplayName: l.DisplayName, Kind: kind, Agent: l.Agent}
		if kind == "api" {
			u.Agent, u.API = "", cmp.Or(l.API, "messages")
		}
		switch {
		case kind == "api" && !messagesAPI(l.API):
			u.Code, u.Reason = unavailableAPIProtocol, apiProtocolText(lang, l.API)
		case err == nil && !s.modelProviderCompatible(p, ModelProvider{ID: "launcher." + l.Name, Kind: kind, Agent: l.Agent, API: l.API}, stored):
			u.Code, u.Reason = unavailableNotMainChat, notMainChat
		case s.scopeReason(l, machine) != "":
			u.Code, u.Reason = proto.CodeLauncherOutOfScope, s.scopeText(ctx, lang, s.scopeReason(l, machine), l, machine)
		case !s.usableOn(ctx, l, machine): // its own-service rule, as the node said
			u.Code, u.Reason = proto.CodeLauncherOwnService, s.ownServiceErr(ctx, lang, l, machine).Error()
		default:
			continue
		}
		out = append(out, u)
	}
	return out
}

// apiProtocolText says in lang why a direct API provider speaking api can't run the assistant's conversations.
func apiProtocolText(lang i18n.Lang, api string) string {
	switch api {
	case "responses":
		return i18n.T(lang, "assistant-model_pick.unavailable_api_responses")
	case "chat":
		return i18n.T(lang, "assistant-model_pick.unavailable_api_chat")
	}
	return i18n.T(lang, "assistant-model_pick.unavailable_api_other", i18n.Args{"api": api})
}
