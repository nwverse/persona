package assistant

import (
	"context"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// A dispatch guard limits only the conversations its targets name. It is off by default: no stored policy, an
// explicitly disabled one and an expired one leave both the tool list and every start path as they were. An enabled
// policy that cannot be read or validated fails closed for dispatch tools, with one fixed refusal that carries none
// of the policy, target, tool input or underlying error.
const (
	dispatchGuardSetting     = "assistant.dispatch_guard"
	dispatchGuardRefusalText = "Dispatch is refused by the configured guard."
	dispatchGuardMaxTargets  = 32
	dispatchGuardMaxIDRunes  = 256
	dispatchGuardMaxExpiry   = time.Hour
	dispatchGuardRoute       = "/api/v1/assistant/dispatch-guard"
)

var dispatchGuardToolNames = append([]string(nil), assistantwire.DispatchGuardTools...)

// DispatchGuardTarget names one assistant conversation on one machine and the dispatch tools it may not call.
type DispatchGuardTarget struct {
	MachineID string   `json:"machine_id"`
	SessionID string   `json:"session_id"`
	Deny      []string `json:"deny"`
}

// DispatchGuard is the stored policy. ExpiresAt is required for an enabled policy written by an extension, and must
// be at most one hour ahead. A full credential may store one without an expiry.
type DispatchGuard struct {
	Enabled   bool                  `json:"enabled"`
	ExpiresAt *time.Time            `json:"expires_at,omitempty"`
	Targets   []DispatchGuardTarget `json:"targets"`
}

type dispatchGuardBook struct {
	mu    sync.Mutex
	set   bool
	value DispatchGuard
}

func (g DispatchGuard) clone() DispatchGuard {
	out := g
	out.Targets = make([]DispatchGuardTarget, len(g.Targets))
	for i, target := range g.Targets {
		out.Targets[i] = target
		out.Targets[i].Deny = append([]string(nil), target.Deny...)
	}
	return out
}

func validGuardID(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && len([]rune(s)) <= dispatchGuardMaxIDRunes &&
		!strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// validate checks a policy before storing or using it. allowNoExpiry distinguishes full credentials from extensions.
func (g DispatchGuard) validate(allowNoExpiry bool, now time.Time) error {
	if g.Enabled && len(g.Targets) == 0 {
		return errors.New("an enabled dispatch guard needs at least one target")
	}
	if len(g.Targets) > dispatchGuardMaxTargets {
		return fmt.Errorf("dispatch_guard.targets has %d entries; at most %d", len(g.Targets), dispatchGuardMaxTargets)
	}
	seenTargets := map[string]bool{}
	for i, target := range g.Targets {
		if !validGuardID(target.MachineID) || !validGuardID(target.SessionID) {
			return fmt.Errorf("dispatch_guard.targets[%d] needs non-empty machine_id and session_id", i)
		}
		key := target.MachineID + "\x00" + target.SessionID
		if seenTargets[key] {
			return fmt.Errorf("dispatch_guard.targets[%d] repeats a target", i)
		}
		seenTargets[key] = true
		if len(target.Deny) == 0 {
			return fmt.Errorf("dispatch_guard.targets[%d].deny is empty", i)
		}
		seenTools := map[string]bool{}
		for _, name := range target.Deny {
			if !slices.Contains(dispatchGuardToolNames, name) || seenTools[name] {
				return fmt.Errorf("dispatch_guard.targets[%d].deny has an unsupported or repeated entry", i)
			}
			seenTools[name] = true
		}
	}
	if g.ExpiresAt != nil {
		if !g.ExpiresAt.After(now) {
			return errors.New("dispatch_guard.expires_at must be in the future")
		}
		if !allowNoExpiry && g.ExpiresAt.After(now.Add(dispatchGuardMaxExpiry)) {
			return errors.New("dispatch_guard.expires_at must be at most one hour ahead")
		}
	} else if !allowNoExpiry && g.Enabled && len(g.Targets) > 0 {
		return errors.New("an enabled dispatch guard written by an extension needs expires_at")
	}
	return nil
}

func (s *Service) loadDispatchGuard(ctx context.Context) (DispatchGuard, bool, error) {
	if st := s.store(); st != nil {
		var g DispatchGuard
		ok, err := st.Setting(ctx, dispatchGuardSetting, &g)
		return g, ok, err
	}
	book := &s.guard
	book.mu.Lock()
	defer book.mu.Unlock()
	return book.value.clone(), book.set, nil
}

func (s *Service) saveDispatchGuard(ctx context.Context, g DispatchGuard) error {
	g = g.clone()
	if g.Targets == nil {
		g.Targets = []DispatchGuardTarget{}
	}
	if st := s.store(); st != nil {
		return st.PutSetting(ctx, dispatchGuardSetting, g)
	}
	book := &s.guard
	book.mu.Lock()
	book.value, book.set = g, true
	book.mu.Unlock()
	return nil
}

type dispatchGuardDenials map[string]bool

func allDispatchGuardTools() dispatchGuardDenials {
	out := make(map[string]bool, len(dispatchGuardToolNames))
	for _, name := range dispatchGuardToolNames {
		out[name] = true
	}
	return out
}

// guardDenied is the one decision shared by the visible tool list and call admission. A read or validation failure
// denies every dispatch tool; unrelated tools and non-target conversations with a valid policy are left alone.
func (s *Service) guardDenied(ctx context.Context, machine, session string) dispatchGuardDenials {
	g, ok, err := s.loadDispatchGuard(ctx)
	now := time.Now().UTC()
	if err != nil {
		s.logf("assistant: dispatch guard unavailable; dispatch tools fail closed")
		return allDispatchGuardTools()
	}
	if !ok || !g.Enabled || g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
		return nil
	}
	if err := g.validate(true, now); err != nil {
		s.logf("assistant: invalid enabled dispatch guard; dispatch tools fail closed")
		return allDispatchGuardTools()
	}
	denied := map[string]bool{}
	for _, target := range g.Targets {
		if target.MachineID != machine || target.SessionID != session {
			continue
		}
		for _, name := range target.Deny {
			denied[name] = true
		}
	}
	return denied
}

// guardRefusal is the model-entry admission check. It runs before the called tool parses its arguments and never
// includes the tool name, arguments, target or policy in its error.
func (s *Service) guardRefusal(ctx context.Context, machine, session, tool string) error {
	if !slices.Contains(dispatchGuardToolNames, tool) {
		return nil
	}
	if s.guardDenied(ctx, machine, session)[tool] {
		return errors.New(i18n.T(s.lang(ctx), "assistant-dispatch_guard.refused"))
	}
	return nil
}

// A watch revision follows the effective decisions for this caller, including expiry and storage recovery.
func (denied dispatchGuardDenials) revision(cursor string) string {
	if len(denied) == 0 {
		return cursor
	}
	var mask uint64
	for i, name := range dispatchGuardToolNames {
		if denied[name] {
			mask |= 1 << i
		}
	}
	return cursor + "." + strconv.FormatUint(mask, 16)
}

func (denied dispatchGuardDenials) filter(tools []proto.Tool) []proto.Tool {
	if len(denied) == 0 {
		return tools
	}
	out := make([]proto.Tool, 0, len(tools))
	for _, tool := range tools {
		if !denied[tool.Name] {
			out = append(out, tool)
		}
	}
	return out
}

// ToolsFor is the assistant's own tool list for one conversation.
func (s *Service) ToolsFor(ctx context.Context, machine, session string) []proto.Tool {
	tools := s.Tools(ctx)
	if machine == "" && session == "" {
		return tools
	}
	return s.guardDenied(ctx, machine, session).filter(tools)
}

// readDispatchGuardUpdate handles the write envelope separately from persisted settings, which keep
// their original representation. A missing target list is valid only for an explicit disable.
func readDispatchGuardUpdate(r io.Reader, full bool, now time.Time) (DispatchGuard, error) {
	var in struct {
		Guard *struct {
			Enabled   *bool          `json:"enabled"`
			ExpiresAt *time.Time     `json:"expires_at"`
			Targets   jsontext.Value `json:"targets"`
		} `json:"dispatch_guard"`
	}
	strict := jsonv2.RejectUnknownMembers(true)
	if err := jsonv2.UnmarshalRead(r, &in, strict); err != nil {
		return DispatchGuard{}, err
	}
	if in.Guard == nil || in.Guard.Enabled == nil {
		return DispatchGuard{}, errors.New("expected a dispatch_guard object with enabled")
	}
	g := DispatchGuard{Enabled: *in.Guard.Enabled, ExpiresAt: in.Guard.ExpiresAt}
	if in.Guard.Targets != nil {
		if in.Guard.Targets.Kind() != '[' {
			return DispatchGuard{}, errors.New("dispatch_guard.targets must be an array")
		}
		if err := jsonv2.Unmarshal(in.Guard.Targets, &g.Targets, strict); err != nil {
			return DispatchGuard{}, err
		}
		if len(g.Targets) == 0 {
			g.Enabled = false
		}
	}
	if err := g.validate(full, now); err != nil {
		return DispatchGuard{}, err
	}
	if len(g.Targets) == 0 {
		return DispatchGuard{Targets: []DispatchGuardTarget{}}, nil
	}
	return g, nil
}

func (s *Service) dispatchGuardRoutes() {
	s.handle("GET "+dispatchGuardRoute, func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get(RoleHeader)
		if !fullRole(r) && role != roleExtension {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot read the dispatch guard"))
			return
		}
		g, _, err := s.loadDispatchGuard(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, errors.New("the dispatch guard could not be read"))
			return
		}
		if g.Targets == nil {
			g.Targets = []DispatchGuardTarget{}
		}
		apierr.WriteJSON(w, map[string]any{"dispatch_guard": g})
	})
	s.handle("PUT "+dispatchGuardRoute, func(w http.ResponseWriter, r *http.Request) {
		role := r.Header.Get(RoleHeader)
		full, extension := fullRole(r), role == roleExtension
		if !full && !extension {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change the dispatch guard"))
			return
		}
		g, err := readDispatchGuardUpdate(http.MaxBytesReader(w, r.Body, 1<<20), full, time.Now().UTC())
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if err := s.saveDispatchGuard(r.Context(), g); err != nil {
			apierr.Write(w, http.StatusInternalServerError, errors.New("the dispatch guard could not be saved"))
			return
		}
		s.mcpChanges.notify() // existing sessions must not keep a list made before this policy change
		apierr.WriteJSON(w, map[string]any{"dispatch_guard": g})
	})
}
