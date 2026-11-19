package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// A local alias on another computer is a different provider, even when its name matches Main chat's.
// Resolve inheritance before applying task overrides; an explicit task choice is never substituted.
func (s *Service) taskModelBaseline(ctx context.Context, machine string) (ModelBinding, error) {
	b, err := s.resolveModel(ctx, "assistant")
	if err != nil || machine == "" || machine == s.settings(ctx).MachineID {
		return b, err
	}
	name := strings.TrimPrefix(b.Provider, "launcher.")
	if managed, ok := s.managedNamed(ctx, name); ok && s.usableOn(ctx, managed, machine) {
		return b, nil // a managed provider retains its identity across computers in its scope
	}
	local := ModelBinding{Provider: b.Provider}
	launchers, err := s.assistantLaunchers(ctx, machine)
	if err != nil {
		return local, nil // unknown discovery is not proof that an exact launcher is absent
	}
	if slices.ContainsFunc(launchers, func(l ConfiguredLauncher) bool { return l.Name == name }) {
		return local, nil
	}
	d, _ := s.device(ctx, machine)
	kind := agentKind(s.launcherAgent(ctx, s.settings(ctx).MachineID, name))
	if replacement := s.mainChatInstead(ctx, d, name, kind, launchers); replacement != "" {
		local.Provider = launcherProvider(replacement)
		return local, nil
	}
	// Without an explicit backend choice, a computer's other headless-compatible default is valid too.
	for _, l := range launchers {
		if _, bad := s.launcherUnusable(ctx, machine, l.Name); !bad {
			return ModelBinding{Provider: launcherProvider(l.Name)}, nil
		}
	}
	return local, nil // normal validation reports an authoritative unavailable configuration
}

// Include the selected computer's advertised identity, so a local alias changing gateways starts a new chat.
func (s *Service) taskBindingFingerprint(ctx context.Context, machine string, b ModelBinding) string {
	name := strings.TrimPrefix(b.Provider, "launcher.")
	var identity struct{ Agent, Source, BaseURL, Model string }
	if list, known := s.launchersHere(withUnhidden(ctx, name), machine); known {
		for _, l := range list {
			if l.Name == name {
				identity.Agent, identity.Source, identity.BaseURL, identity.Model = l.Agent, proto.CanonicalLauncherSource(l.Source), l.BaseURL, l.Model
				break
			}
		}
	}
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(append([]byte(s.modelBindingFingerprint(ctx, b)+"\x00"+machine+"\x00"), raw...))
	return hex.EncodeToString(sum[:])
}
