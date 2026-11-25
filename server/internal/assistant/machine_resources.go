package assistant

import (
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

const machineResourcesRoute = "GET /api/v1/assistant/machine-resources"
const roleScoped = "scoped"

// machineResourceRow is the numeric-only machine summary extension code can display. It deliberately contains no
// session, directory, space, connection-error, launcher or other conversational data.
type machineResourceRow struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	State                string   `json:"state"`
	Online               bool     `json:"online"`
	Cores                *int     `json:"cores"`
	CPUBusyPercent       *float64 `json:"cpu_busy_percent"`
	Load1M               *float64 `json:"load_1m"`
	MemoryAvailableBytes *int64   `json:"memory_available_bytes"`
	MemoryTotalBytes     *int64   `json:"memory_total_bytes"`
	SampleAgeSeconds     *float64 `json:"sample_age_seconds"`
	WorkingAgents        *int     `json:"working_agents"`
}

func (s *Service) machineResourceRoutes() {
	s.handle(machineResourcesRoute, s.getMachineResources)
}

func (s *Service) getMachineResources(w http.ResponseWriter, r *http.Request) {
	snapshots, complete, excluded, err := s.machineResourceSnapshots(r.Context(), time.Now())
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, err)
		return
	}
	rows := make([]machineResourceRow, 0, len(snapshots))
	for _, snapshot := range snapshots {
		fact := snapshot.fact
		row := machineResourceRow{
			ID:                   fact.ID,
			Name:                 fact.Name,
			State:                fact.State,
			Online:               snapshot.online,
			Cores:                intFactValue(fact.Cores.Value),
			CPUBusyPercent:       floatFactValue(fact.CPUBusyPercent.Value),
			Load1M:               floatFactValue(fact.Load.Value),
			MemoryAvailableBytes: int64FactValue(fact.MemoryAvailableBytes.Value),
			MemoryTotalBytes:     snapshot.memoryTotalBytes,
			SampleAgeSeconds:     floatFactValue(fact.SampleAgeSeconds.Value),
		}
		switch r.Header.Get(RoleHeader) {
		case roleExtension, roleScoped:
			row.WorkingAgents = snapshot.extensionWorkingAgents
		default:
			row.WorkingAgents = intFactValue(fact.WorkingAgents.Value)
		}
		rows = append(rows, row)
	}
	apierr.WriteJSON(w, map[string]any{"machines": rows, "complete": complete, "excluded_count": excluded})
}

func isMachineResourceSpaceSession(session proto.Session) bool {
	return session.Space != "" || identity.InSpace(session.Cwd)
}

func machineWorkingAgentCounts(sessions []proto.Session) (full, extension int) {
	for _, session := range sessions {
		if session.HiddenByDirectory() || session.Assistant || session.Archived || session.State != proto.StateWorking {
			continue
		}
		full++
		if !isMachineResourceSpaceSession(session) {
			extension++
		}
	}
	return full, extension
}

func intFactValue(value any) *int {
	switch value := value.(type) {
	case int:
		return &value
	case float64:
		v := int(value)
		return &v
	default:
		return nil
	}
}

func int64FactValue(value any) *int64 {
	switch value := value.(type) {
	case int:
		v := int64(value)
		return &v
	case int64:
		return &value
	case float64:
		v := int64(value)
		return &v
	default:
		return nil
	}
}

func floatFactValue(value any) *float64 {
	switch value := value.(type) {
	case float64:
		return &value
	case int:
		v := float64(value)
		return &v
	case int64:
		v := float64(value)
		return &v
	default:
		return nil
	}
}
