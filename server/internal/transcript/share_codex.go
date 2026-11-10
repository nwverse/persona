package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/devicewire"
)

// Codex publication reads complete rollout records, not the clipped Item cache
// or response_item's duplicate model context. History mutation formats without
// authoritative append/branch evidence cannot establish safe continuity.
func projectCodexShare(ctx context.Context, data []byte, source, incarnation string, previous *devicewire.ShareFrontier) (devicewire.ShareProjection, error) {
	const adapter = "codex-events.v1"
	out := devicewire.ShareProjection{Messages: []devicewire.ShareProjectionMessage{}, Omissions: []string{}}
	deny := func(reason string) (devicewire.ShareProjection, error) {
		out.Reason = reason
		out.Messages = []devicewire.ShareProjectionMessage{}
		return out, nil
	}
	if len(data) > ShareSourceLimit {
		return deny("source_limit")
	}
	boundary := bytes.LastIndexByte(data, '\n') + 1
	f := devicewire.ShareFrontier{Incarnation: incarnation, Adapter: adapter, Source: source, Boundary: int64(boundary), EOF: int64(len(data)), PrefixHash: shareDigest(data[:boundary]), PartialHash: shareDigest(data[boundary:]), FirstSeen: map[string]int64{}, Branch: shareDigest([]byte("codex-append-events"))}
	if previous != nil {
		p := previous
		if p.Incarnation != incarnation || p.Adapter != adapter || p.Source != source || p.Branch != f.Branch || p.Boundary < 0 || p.EOF < p.Boundary || p.EOF > int64(len(data)) || p.Boundary > int64(boundary) {
			return deny("source_changed")
		}
		if shareDigest(data[:p.Boundary]) != p.PrefixHash || shareDigest(data[p.Boundary:p.EOF]) != p.PartialHash {
			return deny("source_changed")
		}
		for id, offset := range p.FirstSeen {
			f.FirstSeen[id] = offset
		}
	}
	omit := func(reason string) {
		out.Omitted++
		for _, r := range out.Omissions {
			if r == reason {
				return
			}
		}
		out.Omissions = append(out.Omissions, reason)
	}
	seen := map[string]string{}
	type turn struct {
		id    string
		first int64
		texts []string
		work  []devicewire.ShareExecution
		bad   bool
	}
	var current *turn
	finish := func() bool {
		if current == nil {
			return true
		}
		defer func() { current = nil }()
		if current.bad || len(current.texts) == 0 {
			omit("component_incomplete")
			return true
		}
		segments, err := sanitizeShareSegments(current.texts)
		if err != nil {
			omit("component_limit")
			return true
		}
		final := segments[len(segments)-1]
		for _, clean := range segments[:len(segments)-1] {
			current.work = append(current.work, devicewire.ShareExecution{Kind: "text", Text: ShareDisplayText(clean)})
		}
		identity := shareDigest([]byte("turn:" + current.id))
		first := current.first
		if old, ok := f.FirstSeen[identity]; ok {
			first = old
		} else {
			f.FirstSeen[identity] = first
		}
		out.Messages = append(out.Messages, devicewire.ShareProjectionMessage{Identity: identity, FirstSeen: first, Message: devicewire.PublicShareMessage{Role: "assistant", Text: ShareDisplayText(final), Execution: current.work}})
		return len(out.Messages) <= ShareLogicalLimit
	}
	for start := 0; start < boundary; {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		end := start + bytes.IndexByte(data[start:boundary], '\n') + 1
		raw := bytes.TrimSpace(data[start:end])
		offset := int64(start)
		start = end
		if len(raw) == 0 {
			continue
		}
		var l codexLine
		if json.Unmarshal(raw, &l) != nil {
			return deny("source_incomplete")
		}
		if l.Type == "history_mutation" {
			return deny("source_unsupported")
		}
		if l.Type != "event_msg" {
			if l.Type == "session_meta" || l.Type == "turn_context" || l.Type == "response_item" {
				continue
			}
			return deny("source_unsupported")
		}
		var p codexPayload
		if json.Unmarshal(l.Payload, &p) != nil {
			return deny("source_incomplete")
		}
		switch p.Type {
		case "task_started":
			if current != nil {
				omit("unfinished_turn")
			}
			id := p.TurnID
			if id == "" {
				id = fmt.Sprintf("offset:%d", offset)
			}
			current = &turn{id: id, first: offset}
			identity := shareDigest([]byte("turn:" + id))
			if _, ok := f.FirstSeen[identity]; !ok {
				f.FirstSeen[identity] = offset
			}
		case "task_complete":
			if current != nil && p.TurnID != "" && current.id != p.TurnID {
				return deny("source_changed")
			}
			if !finish() {
				return deny("source_limit")
			}
		case "item_started":
			if p.Item != nil && p.Item.ID != "" {
				identity := shareDigest([]byte("item:" + p.Item.ID))
				if _, ok := f.FirstSeen[identity]; !ok {
					f.FirstSeen[identity] = offset
				}
			}
		case "item_completed":
			item := p.Item
			if item == nil {
				return deny("source_incomplete")
			}
			if item.ID != "" {
				key := "item:" + item.ID
				digest := shareDigest(l.Payload)
				if old, ok := seen[key]; ok {
					if old != digest {
						return deny("source_changed")
					}
					continue
				}
				seen[key] = digest
			}
			if item.Type == "UserMessage" || item.Type == "user_message" {
				omit("unknown_provenance")
				continue
			}
			if current == nil {
				omit("unassociated_execution")
				continue
			}
			switch item.Type {
			case "AgentMessage", "agent_message":
				var texts []string
				for _, part := range item.Content {
					if part.Type == "text" || part.Type == "output_text" {
						texts = append(texts, part.Text)
					}
				}
				current.texts = append(current.texts, strings.Join(texts, "\n"))
			case "CommandExecution", "command_execution":
				args, _ := json.Marshal(item.Command)
				arguments, err := SanitizeShareArguments(args)
				if err != nil {
					current.bad = true
					continue
				}
				text := item.AggregatedOutput
				if text == "" {
					text = item.Stdout + item.Stderr
				}
				clean, err := SanitizeShareText(text)
				if err != nil {
					current.bad = true
					continue
				}
				current.work = append(current.work, devicewire.ShareExecution{Kind: "tool", ToolID: shareDigest([]byte(item.ID)), Name: "command", Arguments: ShareDisplayText(arguments), Results: []devicewire.ShareResult{{Text: ShareDisplayText(clean)}}})
			case "McpToolCall", "mcp_tool_call":
				name, err := SanitizeShareText(item.Tool)
				if err != nil {
					current.bad = true
					continue
				}
				arguments, err := SanitizeShareArguments(item.Arguments)
				if err != nil {
					current.bad = true
					continue
				}
				var results []devicewire.ShareResult
				if item.Result != nil {
					for _, part := range item.Result.Content {
						if part.Type != "text" {
							continue
						}
						clean, err := SanitizeShareText(part.Text)
						if err != nil {
							current.bad = true
							break
						}
						results = append(results, devicewire.ShareResult{Text: ShareDisplayText(clean)})
					}
				}
				current.work = append(current.work, devicewire.ShareExecution{Kind: "tool", ToolID: shareDigest([]byte(item.ID)), Name: ShareDisplayText(name), Arguments: ShareDisplayText(arguments), Results: results})
			case "Reasoning", "reasoning", "ContextCompaction", "context_compaction":
				// Never publish thinking or model context.
			default:
				omit("unsupported_content")
			}
		case "token_count", "thread_goal_updated", "agent_message_delta", "agent_reasoning_delta", "exec_command_output_delta":
			// Token/model metadata and incomplete deltas are not publication records.
		default:
			omit("unsupported_content")
		}
		if len(f.FirstSeen) > ShareLogicalLimit {
			return deny("source_limit")
		}
	}
	if current != nil {
		omit("unfinished_turn")
	}
	out.Complete = true
	out.Frontier = f
	return out, nil
}
