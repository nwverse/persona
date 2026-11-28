package tokenuse

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Antigravity (agy) keeps each conversation as JSONL steps under
// <config dir>/brain/<id>/.system_generated/logs/transcript.jsonl (or transcript_full.jsonl).
// Terminal PLANNER_RESPONSE steps carry the model and per-request token counts (input_tokens is
// exclusive of cache_read_tokens, matching Tokens); terminal CHECKPOINT steps carry compaction
// call token counts (inheriting the session's latest model when omitted).

const agyAgent = "agy"

// lineParser adapts a line-reading function to Parser.
type lineParser func([]byte) (Record, bool)

func (f lineParser) Line(line []byte) (Record, bool) { return f(line) }

// NewAgyParser reads an Antigravity transcript (transcript.jsonl or transcript_full.jsonl).
func NewAgyParser() Parser {
	var model string
	return lineParser((agyParser{model: &model}).line)
}

type agyParser struct {
	model *string // the session's latest model, for compaction checkpoints without one
}

func (p agyParser) line(line []byte) (Record, bool) {
	if !bytes.Contains(line, []byte(`"PLANNER_RESPONSE"`)) && !bytes.Contains(line, []byte(`"CHECKPOINT"`)) {
		return Record{}, false
	}
	type stepUsage struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadTokens          int64 `json:"cache_read_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheWriteTokens         int64 `json:"cache_write_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheWrite1hTokens       int64 `json:"cache_write_1h_tokens"`
	}
	var e struct {
		Index                    *int       `json:"step_index"`
		Type                     string     `json:"type"`
		Status                   string     `json:"status"`
		CreatedAt                string     `json:"created_at"`
		UpdatedAt                string     `json:"updated_at"`
		Model                    string     `json:"model"`
		InputTokens              int64      `json:"input_tokens"`
		OutputTokens             int64      `json:"output_tokens"`
		CacheReadTokens          int64      `json:"cache_read_tokens"`
		CacheReadInputTokens     int64      `json:"cache_read_input_tokens"`
		CacheWriteTokens         int64      `json:"cache_write_tokens"`
		CacheCreationInputTokens int64      `json:"cache_creation_input_tokens"`
		CacheWrite1hTokens       int64      `json:"cache_write_1h_tokens"`
		Usage                    *stepUsage `json:"usage"`
	}
	if json.Unmarshal(line, &e) != nil {
		return Record{}, false
	}
	if e.Type != "PLANNER_RESPONSE" && e.Type != "CHECKPOINT" {
		return Record{}, false
	}
	switch strings.ToUpper(strings.TrimSpace(e.Status)) {
	case "RUNNING", "PENDING", "IN_PROGRESS", "QUEUED":
		return Record{}, false
	}
	model := strings.TrimSpace(e.Model)
	if strings.HasPrefix(model, "<") && strings.HasSuffix(model, ">") {
		model = ""
	}
	if e.Type == "PLANNER_RESPONSE" && model != "" && p.model != nil {
		*p.model = model
	}
	if e.Type == "CHECKPOINT" && model == "" && p.model != nil {
		model = *p.model
	}

	var u stepUsage
	if e.Usage != nil {
		u = *e.Usage
	}
	in := firstPositive(e.InputTokens, u.InputTokens)
	out := firstPositive(e.OutputTokens, u.OutputTokens)
	cacheRead := firstPositive(e.CacheReadTokens, e.CacheReadInputTokens, u.CacheReadTokens, u.CacheReadInputTokens)
	cacheWrite := firstPositive(e.CacheWriteTokens, e.CacheCreationInputTokens, u.CacheWriteTokens, u.CacheCreationInputTokens)
	cacheWrite1h := firstPositive(e.CacheWrite1hTokens, u.CacheWrite1hTokens)
	if cacheWrite1h > cacheWrite {
		cacheWrite = cacheWrite1h
	}
	t := Tokens{
		Input:        in,
		Output:       out,
		CacheRead:    cacheRead,
		CacheWrite:   cacheWrite,
		CacheWrite1h: cacheWrite1h,
	}
	if t.Zero() {
		return Record{}, false
	}
	ts, rawTime := agyTime(e.CreatedAt, e.UpdatedAt)
	r := Record{Time: ts, Agent: agyAgent, Model: model, Tokens: t}
	if rawTime != "" {
		idx := "-"
		if e.Index != nil {
			idx = strconv.Itoa(*e.Index)
		}
		r.Key = "agy:" + e.Type + ":" + idx + ":" + rawTime + ":" +
			strconv.FormatInt(t.Input, 10) + ":" +
			strconv.FormatInt(t.CacheRead, 10) + ":" +
			strconv.FormatInt(t.CacheWrite, 10) + ":" +
			strconv.FormatInt(t.Output, 10)
	}
	return r, true
}

func agyTime(createdAt, updatedAt string) (time.Time, string) {
	for _, raw := range []string{createdAt, updatedAt} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t, raw
		}
	}
	return time.Time{}, ""
}

func firstPositive(vals ...int64) int64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}
