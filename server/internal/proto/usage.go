package proto

// UsageBucket is one minute of one model's token usage on a machine, summed over every transcript there (node
// usage.go). A node reports a bucket's whole total each time it changes, so reporting it again replaces it. Runtime-only agents instead send per-request deltas
// in a usage notification with add: true; those buckets are added and survive a file-meter full window.
type UsageBucket struct {
	Minute int64  `json:"minute"` // Unix seconds of the minute's start
	Model  string `json:"model"`
	// Launcher ran the replies: the session's launcher as Persona started it, "" when unknown (typed in a terminal, an
	// older node). A minute has one bucket per model, launcher and agent.
	Launcher string `json:"launcher,omitempty"`
	// Agent ran the replies: Session.Agent's names ("claude", "codex", "traex", …; tokenuse); "" from an older node,
	// and for the usage the manager stored before it kept agents apart.
	Agent      string `json:"agent,omitempty"`
	Input      int64  `json:"in,omitempty"` // not from the cache
	CacheWrite int64  `json:"cache_write,omitempty"`
	CacheRead  int64  `json:"cache_read,omitempty"`
	Output     int64  `json:"out,omitempty"`
	Requests   int    `json:"req,omitempty"` // model replies
}

// MethodSessionUsage is the node call that sums sessions' token usage from the records on that computer (#12936):
// params SessionUsageParams, result SessionUsage. The hub never reads another computer's files; it asks the node. It
// names many sessions at once and is bound to none, so it is in the node's method table, not under "session.".
const MethodSessionUsage = "usage.sessions"

// SessionUsageParams are usage.sessions' params: the sessions to sum.
type SessionUsageParams struct {
	SessionIDs []string `json:"session_ids"`
}

// SessionUsage is usage.sessions' answer: which sessions had a record on the computer, and their usage as rows.
type SessionUsage struct {
	Sessions []SessionUsageFound `json:"sessions"`
	Rows     []SessionUsageRow   `json:"rows"`
}

// SessionUsageFound says whether a session asked about had records on the computer, and its agent.
type SessionUsageFound struct {
	SessionID string `json:"session_id"`
	Found     bool   `json:"found"`
	Agent     string `json:"agent,omitempty"`
}

// SessionUsageRow is what one file of a session (its own transcript, or one subagent's) used with one model on one
// day (UTC). The rows keep the session, the subagent and the day, so they can be summed per to-do, per conversation,
// per project or per day; a request copied into more than one file of the same call is counted once.
type SessionUsageRow struct {
	SessionID string `json:"session_id"`
	// Subagent is "" for the session's own transcript, else the subagent's id ("<run>/<id>" for a workflow agent).
	Subagent string `json:"subagent,omitempty"`
	// SubagentType and SubagentTitle are what the subagent's meta says (Claude Code's agentType and description).
	SubagentType  string `json:"subagent_type,omitempty"`
	SubagentTitle string `json:"subagent_title,omitempty"`
	Agent         string `json:"agent"`
	Model         string `json:"model"`
	Day           string `json:"day"` // YYYY-MM-DD, UTC
	Input         int64  `json:"in,omitempty"`
	CacheWrite    int64  `json:"cache_write,omitempty"`
	CacheRead     int64  `json:"cache_read,omitempty"`
	Output        int64  `json:"out,omitempty"`
	Requests      int    `json:"req,omitempty"`
}
