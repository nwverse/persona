package assistantwire

import "time"

// CapabilityAgentSessionIdleArchive advertises the manager's guarded idle-session archive-and-close API and its audit
// read endpoint.
const CapabilityAgentSessionIdleArchive = "agent_session_idle_archive"

const (
	RouteAgentIdleArchive      = "POST /api/v1/sessions/{mid}/{sid}/archive-idle"
	RouteAgentIdleArchiveAudit = "GET /api/v1/sessions/archive-idle/audit"
)

// IdleArchiveRequest asks the manager to archive an idle coding-agent session (and optionally close its terminal) only
// after strictly verifying it has been idle for more than IdleMinutes and is not protected or busy.
type IdleArchiveRequest struct {
	IdleMinutes   int    `json:"idle_minutes"`
	CloseTerminal *bool  `json:"close_terminal,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// IdleArchiveCloseResult describes what happened to the session's running agent/terminal when CloseTerminal was requested.
type IdleArchiveCloseResult struct {
	Closed  bool   `json:"closed"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Named ID types for idle-archive wire contracts (contract-bare-id).
type (
	IdleArchiveAuditID string
	SessionID          string
)

// IdleArchiveResponse is returned on HTTP 200 after a guarded idle archive succeeds (or is already archived offline).
type IdleArchiveResponse struct {
	OK                  bool                    `json:"ok"`
	Archived            bool                    `json:"archived"`
	AlreadyArchived     bool                    `json:"already_archived,omitempty"`
	ObservedIdleSeconds int64                   `json:"observed_idle_seconds"`
	Close               *IdleArchiveCloseResult `json:"close,omitempty"`
	AuditID             IdleArchiveAuditID      `json:"audit_id"`
	AuditPersisted      bool                    `json:"audit_persisted"`
}

// IdleArchiveAuditEntry is one persisted audit record of a guarded idle archive-and-close request (succeeded or refused).
// It keeps only routing, timing, actor, and outcome metadata—never prompts, transcripts, or credentials.
type IdleArchiveAuditEntry struct {
	ID                  IdleArchiveAuditID `json:"id"`
	At                  time.Time          `json:"at"`
	Actor               string             `json:"actor"`
	Extension           string             `json:"extension,omitempty"`
	MachineID           MachineID          `json:"machine_id"`
	SessionID           SessionID          `json:"session_id"`
	IdleMinutes         int                `json:"idle_minutes"`
	ObservedIdleSeconds *int64             `json:"observed_idle_seconds,omitempty"`
	CloseTerminal       bool               `json:"close_terminal"`
	Outcome             string             `json:"outcome"`
	RefusalCode         string             `json:"refusal_code,omitempty"`
	CloseOutcome        string             `json:"close_outcome,omitempty"`
	Reason              string             `json:"reason,omitempty"`
}

func init() {
	Routes = append(Routes,
		RouteAgentIdleArchive,
		RouteAgentIdleArchiveAudit,
	)
}
