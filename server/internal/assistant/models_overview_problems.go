package assistant

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

type modelOverviewProblem struct {
	Code     string             `json:"code"`
	Severity string             `json:"severity"`
	Machine  string             `json:"machine"`
	Purpose  string             `json:"purpose"`
	Launcher string             `json:"launcher"`
	Tier     string             `json:"tier"`
	Text     string             `json:"text"`
	Fixes    []modelOverviewFix `json:"fixes"`
}

type modelOverviewFix struct {
	Action string `json:"action"`
	Target string `json:"target"`
}

type overviewProblemList struct {
	lang  i18n.Lang
	items []modelOverviewProblem
}

func (p *overviewProblemList) add(problem modelOverviewProblem, text string, args i18n.Args, fixes ...modelOverviewFix) {
	problem.Text = mc(p.lang, "overview_problem_"+cmp.Or(text, problem.Code), args)
	problem.Fixes = fixes
	p.items = append(p.items, problem)
}

func (s *Service) overviewProblems(ctx context.Context, data modelOverviewData, purposes []overviewPurpose) []modelOverviewProblem {
	out := overviewProblemList{lang: s.requestCtxLang(ctx), items: []modelOverviewProblem{}}
	byPurpose := make(map[string]ModelPurpose, len(purposes))
	for _, purpose := range purposes {
		p := purpose.purpose
		byPurpose[p.ID] = p
		out.addPurpose(purpose)
	}
	now := time.Now()
	for _, machine := range data.Machines {
		out.addMachine(machine, now)
	}
	out.addHidden(data.Providers, byPurpose, data.Profile)
	out.addProfile(data.Profile, data.Machines)

	severity := map[string]int{"error": 0, "warning": 1, "info": 2}
	slices.SortFunc(out.items, func(a, b modelOverviewProblem) int {
		return cmp.Or(cmp.Compare(severity[a.Severity], severity[b.Severity]),
			cmp.Compare(a.Code, b.Code), cmp.Compare(a.Machine, b.Machine),
			cmp.Compare(a.Purpose, b.Purpose), cmp.Compare(a.Launcher, b.Launcher), cmp.Compare(a.Tier, b.Tier))
	})
	return out.items
}

func (p *overviewProblemList) addPurpose(use overviewPurpose) {
	purpose := use.purpose
	machine := use.runsOn
	if machine == "each" || machine == "api" {
		machine = ""
	}
	switch purpose.StatusCode {
	case modelStatusNoAPI, modelStatusNoModel, modelStatusProviderIncompatible, modelStatusOutOfScope:
		p.add(modelOverviewProblem{Code: string(purpose.StatusCode), Severity: "error", Machine: machine, Purpose: purpose.ID},
			"", i18n.Args{"purpose": i18n.Verbatim(purpose.Name)}, modelOverviewFix{"change_purpose", modelRecordID(purpose)})
	case modelStatusNoCredentials:
		launcher, managed := strings.CutPrefix(use.binding.Provider, "launcher.")
		fix := modelOverviewFix{"change_purpose", modelRecordID(purpose)}
		if managed {
			fix = modelOverviewFix{"edit_provider", launcher}
		} else {
			launcher = ""
		}
		p.add(modelOverviewProblem{Code: "no_credentials", Severity: "error", Machine: machine, Purpose: purpose.ID, Launcher: launcher},
			"", i18n.Args{"purpose": i18n.Verbatim(purpose.Name)}, fix)
	}
}

func (p *overviewProblemList) addMachine(machine modelOverviewMachine, now time.Time) {
	args := i18n.Args{"machine": i18n.Verbatim(machine.Name)}
	machineFix := modelOverviewFix{"open_machine", machine.ID}
	switch machine.List {
	case "none":
		p.add(modelOverviewProblem{Code: "list_unknown", Severity: "info", Machine: machine.ID},
			"", args, modelOverviewFix{"refresh_list", machine.ID})
	case "stale":
		p.add(modelOverviewProblem{Code: "list_stale", Severity: "info", Machine: machine.ID},
			"", args, modelOverviewFix{"refresh_list", machine.ID})
	}
	if machine.Online && machine.Node.Version != "" && !machine.Node.ModelConfiguration {
		p.add(modelOverviewProblem{Code: "node_outdated", Severity: "warning", Machine: machine.ID}, "", args, machineFix)
	}
	if machine.Online && machine.List == "fresh" && machine.ClaudeVersion == "" && machine.catalogKnown &&
		!slices.ContainsFunc(machine.Providers, func(provider modelOverviewProvider) bool {
			return provider.InPicker || provider.hiddenUsable
		}) {
		p.add(modelOverviewProblem{Code: "nothing_to_run", Severity: "error", Machine: machine.ID}, "", args, machineFix)
	}
	p.addAgentFit(machine)
	for _, chat := range machine.Chats {
		if chat.Purpose == "assistant" {
			p.addMainChat(machine, chat)
		}
	}
	p.addProviderHealth(machine, now)
}

func (p *overviewProblemList) addAgentFit(machine modelOverviewMachine) {
	fit := machine.NewAgent
	change := modelOverviewFix{"change_purpose", "agent.new"}
	if fit.InsteadOf != "" && fit.Launcher != "" {
		text := "default_fallback"
		if machine.List == "stale" {
			text = "default_fallback_stale"
		}
		p.add(modelOverviewProblem{Code: "default_fallback", Severity: "warning", Machine: machine.ID, Purpose: "agent.new", Launcher: fit.InsteadOf},
			text, i18n.Args{"machine": i18n.Verbatim(machine.Name), "launcher": i18n.Verbatim(fit.InsteadOf), "to": i18n.Verbatim(fit.Launcher)},
			change, modelOverviewFix{"edit_provider", fit.InsteadOf})
	}
	args := i18n.Args{"machine": i18n.Verbatim(machine.Name), "launcher": i18n.Verbatim(fit.Launcher)}
	switch fit.Fit {
	case fitBlocked:
		p.add(modelOverviewProblem{Code: "default_blocked", Severity: "error", Machine: machine.ID, Purpose: "agent.new", Launcher: fit.Launcher},
			"", args, modelOverviewFix{"edit_provider", fit.Launcher}, change)
	case fitFailing:
		p.add(modelOverviewProblem{Code: "default_failing", Severity: "error", Machine: machine.ID, Purpose: "agent.new", Launcher: fit.Launcher},
			"", args, modelOverviewFix{"test", fit.Launcher}, modelOverviewFix{"edit_provider", fit.Launcher})
	}
}

func (p *overviewProblemList) addMainChat(machine modelOverviewMachine, chat modelOverviewChat) {
	args := i18n.Args{"machine": i18n.Verbatim(machine.Name), "launcher": i18n.Verbatim(chat.Launcher)}
	change := modelOverviewFix{"change_purpose", chat.Purpose}
	if !machine.Online {
		p.add(modelOverviewProblem{Code: "main_chat_offline", Severity: "error", Machine: machine.ID, Purpose: chat.Purpose, Launcher: chat.Launcher},
			"", args, modelOverviewFix{"open_machine", machine.ID})
	}
	switch chat.Fit {
	case fitFallback:
		text := "main_chat_fallback"
		if machine.List == "stale" {
			text = "main_chat_fallback_stale"
		}
		args["launcher"], args["to"] = i18n.Verbatim(chat.InsteadOf), i18n.Verbatim(chat.Launcher)
		p.add(modelOverviewProblem{Code: "main_chat_fallback", Severity: "warning", Machine: machine.ID, Purpose: chat.Purpose, Launcher: chat.InsteadOf},
			text, args, change)
	case fitChatRefused:
		text := "main_chat_refused"
		if machine.List == "stale" {
			text = "main_chat_refused_stale"
		}
		p.add(modelOverviewProblem{Code: "main_chat_refused", Severity: "error", Machine: machine.ID, Purpose: chat.Purpose, Launcher: chat.Launcher},
			text, args, change)
	}
}

func (p *overviewProblemList) addProviderHealth(machine modelOverviewMachine, now time.Time) {
	for _, provider := range machine.Providers {
		if !provider.InScope {
			continue
		}
		fix := modelOverviewFix{"edit_provider", provider.Name}
		args := i18n.Args{"machine": i18n.Verbatim(machine.Name), "launcher": i18n.Verbatim(provider.DisplayName)}
		if provider.missingKey {
			p.add(modelOverviewProblem{Code: "no_credentials", Severity: "error", Machine: machine.ID, Launcher: provider.Name},
				"no_provider_key", args, fix)
		}
		if health := provider.Health; health != nil && health.ResetsAt != nil && now.Before(*health.ResetsAt) {
			p.add(modelOverviewProblem{Code: "usage_limit", Severity: "warning", Machine: machine.ID, Launcher: provider.Name},
				"", args, fix)
		}
	}
}

func (p *overviewProblemList) addHidden(providers map[string]modelOverviewProviderIndex, purposes map[string]ModelPurpose, profile *modelOverviewProfile) {
	for id, provider := range providers {
		launcher, ok := strings.CutPrefix(id, "launcher.")
		if !ok || !provider.Hidden || launcher == "claude" {
			continue
		}
		for _, use := range provider.UsedBy {
			args := i18n.Args{"launcher": i18n.Verbatim(launcher)}
			fix := modelOverviewFix{"unhide", launcher}
			if tier, isTier := strings.CutPrefix(use, "tier:"); isTier && profile != nil {
				args["tier"] = i18n.Verbatim(tier)
				p.add(modelOverviewProblem{Code: "hidden_in_use", Severity: "warning", Launcher: launcher, Tier: tier},
					"hidden_tier_in_use", args, fix, modelOverviewFix{"open_profile", profile.ID})
			} else if purpose, ok := purposes[use]; ok {
				args["purpose"] = i18n.Verbatim(purpose.Name)
				p.add(modelOverviewProblem{Code: "hidden_in_use", Severity: "warning", Launcher: launcher, Purpose: use},
					"", args, fix, modelOverviewFix{"change_purpose", modelRecordID(purpose)})
			}
		}
	}
}

func (p *overviewProblemList) addProfile(profile *modelOverviewProfile, machines []modelOverviewMachine) {
	if profile == nil {
		return
	}
	fix := modelOverviewFix{"open_profile", profile.ID}
	if profile.Drift > 0 {
		p.add(modelOverviewProblem{Code: "profile_drift", Severity: "info"}, "",
			i18n.Args{"profile": i18n.Verbatim(profile.Name), "count": profile.Drift}, fix)
	}
	for _, tier := range profile.Tiers {
		missing := 0
		for _, machine := range machines {
			if slices.Contains(tier.On, machine.ID) {
				continue
			}
			for _, fit := range machine.Tiers {
				if fit.Name == tier.Name && fit.known && !fit.Available {
					missing++
					break
				}
			}
		}
		if missing == 0 {
			continue
		}
		severity := "warning"
		if missing == len(machines) {
			severity = "error"
		}
		p.add(modelOverviewProblem{Code: "tier_missing", Severity: severity, Launcher: tier.Launcher, Tier: tier.Name},
			"", i18n.Args{"tier": i18n.Verbatim(tier.Name), "count": missing}, fix)
	}
}
