package transcript

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A turn that ended on a model API error, as Claude Code's own record says it (Meta.TurnError…, #1710): the reply it
// writes itself for a request that failed (an assistant line with isApiErrorMessage, model <synthetic>) names the
// error's kind, and a subscription's limit when it resets (quotaLimits). Its notices say whether it goes on by itself
// once the limit resets. Codex's record is read in codex.go (task_complete's error, the rollout's rate limits).

// ClaudeTurnStarted: a Claude turn starts, with a message or a background task waking the session: the last turn's
// error is over. Claude Code's word that it goes on by itself after a limit (TurnErrorResumes) outlasts a wake, which
// fails again at once with no new notice, but not a message: typing into the session cancels its continuation without
// a word. The node calls it for a prompt line too big to parse.
func (m *Meta) ClaudeTurnStarted(wake bool) {
	resumes := m.TurnErrorResumes && wake
	m.clearTurnError()
	m.TurnErrorResumes, m.turnWorked = resumes, false
}

func (m *Meta) clearTurnError() {
	m.TurnError, m.TurnErrorKind, m.TurnErrorResetsAt = "", "", time.Time{}
	m.TurnErrorResumes, m.TurnErrorAfterWork, m.turnErrorLine = false, false, ""
}

// claudeReply notes what an assistant line says of the turn; blocks is its content, shown whether it showed anything.
// A reply from the model ends whatever failed before (a limit included: it is over); Claude Code's own reply for a
// failed request is the turn's error. In a session's transcript a subagent's lines (isSidechain) never get here.
func (m *Meta) claudeReply(e *entry, blocks []block, shown bool) {
	if !e.IsAPIErrorMessage {
		if shown && e.Message.Model != "<synthetic>" {
			m.clearTurnError()
			m.turnWorked = true
		}
		return
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	var code string
	_ = json.Unmarshal(e.Error, &code)
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		text = code
	}
	if text == "" {
		return
	}
	kind, reset := claudeErrorKind(code, e)
	if kind != proto.TurnErrorUsageLimit {
		m.TurnErrorResumes = false // only a limit's wait ends in Claude Code's own continuation
	}
	m.TurnError, m.TurnErrorKind, m.TurnErrorResetsAt = text, kind, reset
	m.TurnErrorAfterWork, m.turnErrorLine = m.turnWorked, e.UUID
}

// claudeErrorKind sorts Claude Code's error field (2.1.2xx+): a subscription's limit (rate_limit with the account's
// quota rejected) and when it resets; sign-in and the model are the user's to fix; the service failing is worth a
// "Continue". Anything else gives no kind: an older version wrote no field, a gateway's 429 is a rate_limit without
// quotaLimits (its words say what it is), unknown is anything.
func claudeErrorKind(code string, e *entry) (kind string, reset time.Time) {
	switch code {
	case "rate_limit":
		if q := e.QuotaLimits; q != nil && q.Status == "rejected" {
			return proto.TurnErrorUsageLimit, epochTime(q.ResetsAt)
		}
	case "authentication_failed", "model_not_found":
		return proto.TurnErrorUser, time.Time{}
	case "server_error":
		return proto.TurnErrorTransient, time.Time{}
	}
	return "", time.Time{}
}

// epochTime reads a time written as epoch seconds (milliseconds when it is that large); zero for 0, null or anything
// else.
func epochTime(raw json.RawMessage) time.Time {
	var f float64
	if json.Unmarshal(raw, &f) != nil || f <= 0 {
		return time.Time{}
	}
	if f > 1e11 {
		return time.UnixMilli(int64(f)).UTC()
	}
	return time.Unix(int64(f), 0).UTC()
}

// claudeLimitNotice reads Claude Code's notices on going on by itself after a usage limit (system/informational,
// 2.1.282+; the texts can change by remote config, the words matched are the ones that stay). "Usage limit reached ·
// continuing automatically at 4:20pm · esc or type to cancel" (and "… reached again …") say it will; "Automatic
// continue cancelled / was turned off / stopped / did not run …" and "Usage limit has reset · Press enter to continue"
// that it will not. "Usage limit reset · continuing automatically" and "Usage limit available again · continuing now"
// are it doing so: nothing changes until its first reply clears the error.
func (m *Meta) claudeLimitNotice(text string) {
	t := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.HasPrefix(t, "automatic continue"), strings.Contains(t, "press enter to continue"):
		m.TurnErrorResumes = false
	case strings.HasPrefix(t, "usage limit reset"), strings.HasPrefix(t, "usage limit available again"):
	case strings.Contains(t, "continuing automatically"):
		m.TurnErrorResumes = true
	}
}

// SettleTurnError takes the turn's error out of meta when the line that said it lies on a branch gone back over (a
// rewind in the terminal or from the app): the conversation that is left did not fail there. Nothing older comes back:
// the user went back to start over.
func (t *Tail) SettleTurnError(meta *Meta) {
	if meta.turnErrorLine != "" && t.tree.hidden[meta.turnErrorLine] {
		meta.clearTurnError()
	}
}
