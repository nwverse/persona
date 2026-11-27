package node

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

// Tests replace these reads; production uses the native summary and a fresh terminal screen.
var agySummaryStatusFn, agyStatusScreenFn = readAgySummaryStatus, (*Node).dialogScreen

func (n *Node) agyStatus(id string, a host.Agent, now time.Time) string {
	if a.AgentStatus == proto.StatusBlocked {
		status := n.agySelfHealBlocked(id, a, now)
		if status == "" {
			return proto.StatusBlocked // A recent send never hides an unverified permission dialog.
		}
		a.AgentStatus = status
	} else {
		n.noteBlockedAt(id, false, now)
	}
	if n.workSent.recent(a.PaneID, now) {
		return proto.StatusWorking
	}
	return a.AgentStatus
}

// A sticky OSC 7501 blocked is healed only when all three independent sources agree.
func (n *Node) agySelfHealBlocked(id string, a host.Agent, now time.Time) string {
	since := n.noteBlockedAt(id, true, now)
	n.mu.Lock()
	report := n.agyReports[a.PaneID]
	conversation := n.agyReportedLocked(a)
	n.mu.Unlock()
	settled := report.event == "Stop" && report.settled
	advanced := report.event == "PreInvocation" && report.invocationSteps > 0 && report.at.After(since)
	if host.ParsePane(a.PaneID).Kind != host.HerdrCore || !uuidRe.MatchString(conversation) ||
		conversation != report.conversation || !settled && !advanced {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, notIdle, err := agySummaryStatusFn(ctx, n.agyRootKnownFor(conversation), conversation)
	if err != nil || notIdle || status != "CASCADE_RUN_STATUS_IDLE" && status != "IDLE" && status != "DONE" {
		return ""
	}
	text, err := agyStatusScreenFn(n, ctx, a.PaneID)
	plain := screen.Plain(text)
	lower := strings.ToLower(plain)
	if err != nil || screen.ParseDialog(plain) != nil || screen.Choosing(plain) ||
		strings.Contains(lower, "allow ") || strings.Contains(lower, "esc to cancel") || agyStartNotice(plain) {
		return ""
	}
	rows := contentRows(screenRows(plain))
	ready := agyInputReady(plain)
	if len(rows) > 0 {
		prompt := strings.TrimSpace(rows[len(rows)-1])
		ready = ready || prompt == "❯" || prompt == ">"
	}
	n.mu.Lock()
	unchanged := n.agyReports[a.PaneID] == report && n.agyReportedLocked(a) == conversation
	n.mu.Unlock()
	if !ready || !unchanged {
		return ""
	}
	if settled {
		return proto.StatusDone
	}
	return proto.StatusIdle
}

func readAgySummaryStatus(ctx context.Context, root, conversation string) (string, bool, error) {
	sqlite, err := codexSQLiteProgram()
	if err != nil {
		return "", true, err
	}
	query := "SELECT status, not_fully_idle FROM conversation_summaries WHERE conversation_id = '" +
		strings.ReplaceAll(conversation, "'", "''") + "'"
	out, err := exec.CommandContext(ctx, sqlite, "-readonly", "-bail", "-json", "-cmd", ".timeout 100",
		filepath.Join(root, "conversation_summaries.db"), query).Output()
	var rows []struct {
		Status       string `json:"status"`
		NotFullyIdle *int   `json:"not_fully_idle"`
	}
	if err != nil {
		return "", true, err
	}
	if err = json.Unmarshal(out, &rows); err != nil || len(rows) != 1 || rows[0].NotFullyIdle == nil {
		return "", true, err
	}
	return rows[0].Status, *rows[0].NotFullyIdle != 0, nil
}
