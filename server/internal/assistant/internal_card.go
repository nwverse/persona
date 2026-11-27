package assistant

import (
 "context"
 "encoding/json"
 "slices"
 "strings"
 "time"

 "github.com/nwverse/persona/server/internal/proto"
)

// questionOwnerSource reads decision rights at the ask_user source. The metadata is
// kept in ToolInput, so asynchronous questions retain it across manager restarts.
func questionOwnerSource(ix proto.Interaction) string {
 var rights struct {
  WhyOwner string `json:"why_owner"`
  OwnerAction string `json:"owner_action"`
 }
 _ = json.Unmarshal(ix.ToolInput, &rights)
 switch rights.OwnerAction {
 case "personal_unlock", "login", "second_factor", "experience_check":
  return cardSecretForm
 case "spending":
  return cardPlanMoney
 }
 if slices.Contains(whyOwners, rights.WhyOwner) {
  return decisionCardSource(rights.WhyOwner)
 }
 // Older callers cannot label personal unlocks. Keep this narrow safety fallback
 // while the updated tool contract asks for a typed action; never request a key.
 text := strings.ToLower(string(ix.ToolInput))
 if strings.Contains(text, "1password") || strings.Contains(text, "private key") || strings.Contains(text, "私钥") {
  return cardSecretForm
 }
 return ""
}

// internalCard admits only decisions within the supervisor's authority. A source
// tag never overrides a secret, an outward write or an owner-only operation.
func (s *Service) internalCard(ix proto.Interaction) bool {
 if secretOrForm(ix) || ix.Private || ix.OutwardWrite || ix.OwnerProposal || ix.ApprovalBatchID != "" {
  return false
 }
 src := s.inputCardSource(ix, false)
 if ix.ToolName == "ask_user" {
  return questionOwnerSource(ix) == ""
 }
 if src == cardConfirmOutbound {
  // Only the owned-Agent path opts in, after the track boundary was checked.
  return ix.ToolName == "agent_send"
 }
 if ix.Kind == proto.IxScreen && (!supervisedScreen(ix) || userScreen(ix) != "") {
  return false
 }
 if ix.Kind == proto.IxPermission && userApproval(ix) != "" {
  return false
 }
 return src == cardAgentPermission || src == cardAgentQuestion || src == cardExtensionApproval ||
  src == cardToolPermission && proto.InternalMaintenanceApproval(ix)
}

// internalReviewer never sends an approval back to the conversation that raised
// it. Main's own cards use a separate, auditable background conversation. Failure
// to create it is closed unanswered, never interpreted as approval or owner work.
func (s *Service) internalReviewer(ctx context.Context, ix proto.Interaction) (string, string) {
 cfg := s.settings(ctx)
 to, _ := s.nonOwnerCardTarget(ctx, ix.MachineID, ix.SessionID)
 if to != "" && to != ix.SessionID {
  return to, s.canonical(cfg.MachineID)
 }
 if ix.SessionID == cfg.Main && cfg.Main != "" {
  s.background.mu.Lock()
  defer s.background.mu.Unlock()
  cfg = s.settings(ctx)
  if cfg.BackgroundSupervisor != "" && cfg.BackgroundSupervisor != ix.SessionID {
   return cfg.BackgroundSupervisor, s.canonical(cfg.MachineID)
  }
  if id, err := s.startBackground(ctx); err == nil && id != ix.SessionID {
   return id, s.canonical(cfg.MachineID)
  }
 }
 return "", s.canonical(cfg.MachineID)
}

func validInternalAnswer(ix proto.Interaction, r proto.Resolution) bool {
 return r.Allow != nil && *r.Allow && !r.Cancel && !r.Dismiss && !r.Late && time.Now().Before(ix.ExpiresAt)
}
