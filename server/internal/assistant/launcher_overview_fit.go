package assistant

import (
	"cmp"
	"slices"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/proto"
)

// launcherFit labels a launch prediction. Scope and health are applied by the
// caller to the launcher that fitOn picks.
type launcherFit string

const (
	fitAsSet     launcherFit = "as_set"
	fitFallback  launcherFit = "fallback"
	fitBlocked   launcherFit = "blocked"
	fitFailing   launcherFit = "failing"
	fitUnchecked launcherFit = "unchecked"
)

// fitOn holds the default launcher of a terminal Agent start to one computer's
// inventory. The inventory includes any named hidden launcher and configured
// wrappers that the start would see. Live starts separately refuse a fallback
// when the caller explicitly named the missing launcher.
func fitOn(m Device, list []proto.Launcher, known bool, name string, everywhere bool) (string, launcherFit) {
	if everywhere {
		return cmp.Or(name, "claude"), fitAsSet
	}
	if !known {
		return name, fitUnchecked
	}
	if slices.ContainsFunc(list, func(l proto.Launcher) bool { return l.Name == name }) {
		return name, fitAsSet
	}
	return launcherInstead(m, list), fitFallback
}

// mainChatCatalogKnown is the corresponding check for an Assistant picker.
// An old entry without source metadata does not prove the catalog came back.
func mainChatCatalogKnown(picker []ConfiguredLauncher) bool {
	return slices.ContainsFunc(picker, func(l ConfiguredLauncher) bool {
		source := compat.Source(l.Source)
		return l.Source != "" && source != "persona" && source != "builtin"
	})
}

// mainChatFitOn classifies a launcher against a known picker and its
// Assistant-compatible subset. A refusal also returns the launcher's label.
func mainChatFitOn(name string, picker, fits []ConfiguredLauncher) (mainChatFit, string) {
	named := func(l ConfiguredLauncher) bool { return l.Name == name }
	if slices.ContainsFunc(fits, named) {
		return mainChatFits, ""
	}
	if i := slices.IndexFunc(picker, named); i >= 0 {
		return mainChatRefused, launcherLabel(picker[i])
	}
	return mainChatGone, ""
}

// mainChatInsteadOn chooses the first configured substitute that can run the
// Assistant, preserves the agent kind and passes the supplied health check.
func mainChatInsteadOn(name, kind string, launchers []ConfiguredLauncher, substitutes []string, unusable func(string) bool) string {
	if kind == "" {
		return "" // no evidence that a substitute is of the same kind
	}
	for _, n := range substitutes {
		if n == name || proto.IsExternalLauncher(n) {
			continue
		}
		i := slices.IndexFunc(launchers, func(l ConfiguredLauncher) bool { return l.Name == n })
		if i < 0 {
			continue
		}
		l := launchers[i]
		if l.Source != proto.ExternalLauncherSource && agentKind(cmp.Or(l.AgentType, l.Agent)) == kind && !unusable(n) {
			return n
		}
	}
	return ""
}
