package transcript

import (
	"bytes"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/proto"
)

// Bounded constants for deterministic transcript-based context category estimation.
const (
	// breakdownSampleLimit bounds per-string UTF-8 inspection so estimation stays O(1) per block.
	breakdownSampleLimit = 4096
	// imageTokenBytesEstimate approximates a single inline image (~1,600 tokens at 4 bytes/token)
	// rather than counting raw base64 payload bytes as text tokens.
	imageTokenBytesEstimate int64 = 6400
	// skippedLineEnvelopeBytes approximates JSON envelope overhead on large skipped lines.
	skippedLineEnvelopeBytes int64 = 256
)

// ContextStats accumulates bounded category byte weights from parsed transcript records since the
// latest compaction in the scanned window. Zero values represent no observed evidence.
type ContextStats struct {
	SystemBytes       int64
	ToolsBytes        int64
	ConversationBytes int64
	ToolOutputBytes   int64
	CompactedInWindow bool
	Native            *proto.ContextBreakdown
}

// Empty reports whether no transcript category bytes or native breakdown have been observed.
func (s ContextStats) Empty() bool {
	return s.SystemBytes == 0 && s.ToolsBytes == 0 && s.ConversationBytes == 0 && s.ToolOutputBytes == 0 && s.Native == nil
}

// ResetCompacted clears pre-compaction category accumulators and marks that compaction occurred
// within the current scan window.
func (s *ContextStats) ResetCompacted() {
	s.SystemBytes = 0
	s.ToolsBytes = 0
	s.ConversationBytes = 0
	s.ToolOutputBytes = 0
	s.CompactedInWindow = true
	s.Native = nil
}

// SetNative stores a sanitized engine-reported exact breakdown when valid.
func (s *ContextStats) SetNative(b *proto.ContextBreakdown) {
	if b == nil {
		return
	}
	cp := b.Clone()
	if cp.Source == "" {
		cp.Source = proto.ContextBreakdownExact
	}
	if san := cp.Sanitized(); san != nil {
		s.Native = san
	}
}

// ObserveSkippedUserLine accounts for a large user transcript line skipped by quickLine without
// full JSON decoding. Base64 image payloads use a bounded image estimate instead of raw byte length.
func (m *Meta) ObserveSkippedUserLine(line []byte) {
	if len(line) == 0 || bytes.Contains(line, []byte(`"isSidechain":true`)) {
		return
	}
	payload := int64(len(line)) - skippedLineEnvelopeBytes
	if payload <= 0 {
		return
	}
	isImage := bytes.Contains(line, []byte(`"base64"`))
	if isImage && payload > imageTokenBytesEstimate {
		payload = imageTokenBytesEstimate
	}
	switch {
	case bytes.Contains(line, []byte(`"tool_result"`)):
		m.ContextStats.ToolOutputBytes += payload
	case bytes.Contains(line, []byte(`"isMeta":true`)) || bytes.Contains(line, []byte(`"isCompactSummary":true`)):
		m.ContextStats.SystemBytes += payload
	default:
		m.ContextStats.ConversationBytes += payload
	}
}

// observeUserText classifies a user text block, separating injected system/context tags from user turns.
func (m *Meta) observeUserText(text string, isMeta bool) {
	if text == "" {
		return
	}
	w := estimateWeightedBytes(text)
	if isMeta || strings.Contains(text, "<system-reminder>") || strings.Contains(text, "<persona-context") {
		m.ContextStats.SystemBytes += w
		return
	}
	m.ContextStats.ConversationBytes += w
}

// observeItemsContext accounts for normalized Codex/generic transcript items in ContextStats.
func (m *Meta) observeItemsContext(items []proto.Item) {
	for _, it := range items {
		switch it.Kind {
		case proto.KindText, proto.KindThinking:
			if it.Role == "user" {
				m.observeUserText(it.Text, false)
			} else if it.Role == "assistant" {
				m.ContextStats.ConversationBytes += estimateWeightedBytes(it.Text)
			}
		case proto.KindToolUse:
			m.ContextStats.ToolOutputBytes += int64(len(it.ToolName)) + estimateWeightedBytes(string(it.ToolInput))
		case proto.KindToolResult:
			m.ContextStats.ToolOutputBytes += estimateWeightedBytes(it.Text)
		case proto.KindImage:
			if it.ToolUseID != "" {
				m.ContextStats.ToolOutputBytes += imageTokenBytesEstimate
			} else {
				m.ContextStats.ConversationBytes += imageTokenBytesEstimate
			}
		}
	}
}

// estimateWeightedBytes converts text into ASCII-equivalent bytes (~4 weighted bytes per token),
// scanning at most breakdownSampleLimit bytes so cost is strictly O(1) bounded.
func estimateWeightedBytes(s string) int64 {
	n := len(s)
	if n == 0 {
		return 0
	}
	sample := s
	if n > breakdownSampleLimit {
		sample = s[:breakdownSampleLimit]
		// Trim trailing incomplete UTF-8 sequence at the sample boundary.
		for len(sample) > 0 && !utf8.ValidString(sample[max(len(sample)-4, 0):]) {
			sample = sample[:len(sample)-1]
		}
		if len(sample) == 0 {
			return int64(n)
		}
	}
	var asciiBytes, nonASCIIRunes int64
	for i := 0; i < len(sample); {
		if sample[i] < utf8.RuneSelf {
			asciiBytes++
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(sample[i:])
		nonASCIIRunes++
		i += size
	}
	// ASCII averages ~4 bytes/token (weight 1/byte); non-ASCII runes average ~1 token/rune (weight 4/rune).
	weightedSample := asciiBytes + nonASCIIRunes*4
	if len(sample) == n {
		return weightedSample
	}
	return (weightedSample * int64(n)) / int64(len(sample))
}

// EstimateTokens converts weighted bytes to a bounded, deterministic token count (~4 bytes/token).
func EstimateTokens(weightedBytes int64) int {
	if weightedBytes <= 0 {
		return 0
	}
	tokens := (weightedBytes + 3) / 4
	if tokens > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(tokens)
}

// BuildContextBreakdown constructs an honest proto.ContextBreakdown from observed transcript stats
// and real session context counts. It prefers native exact breakdowns, falls back to deterministic
// transcript-based estimation marked "estimated", and returns nil when no reliable data exists.
func BuildContextBreakdown(stats ContextStats, contextTokens, contextWindow, autoCompactThreshold int, fullWindow bool) *proto.ContextBreakdown {
	if contextWindow > 0 && autoCompactThreshold > contextWindow {
		autoCompactThreshold = contextWindow
	}
	if stats.Native != nil {
		if b := stats.Native.Sanitized(); b != nil {
			if b.Free == nil && contextWindow > 0 && contextTokens > 0 {
				b.Free = proto.Int(max(contextWindow-contextTokens, 0))
			}
			if b.AutoCompactThreshold == nil && autoCompactThreshold > 0 {
				b.AutoCompactThreshold = proto.Int(autoCompactThreshold)
			}
			return b
		}
	}
	if contextTokens <= 0 {
		return nil
	}
	estSys := EstimateTokens(stats.SystemBytes)
	estTools := EstimateTokens(stats.ToolsBytes)
	estConv := EstimateTokens(stats.ConversationBytes)
	estToolOut := EstimateTokens(stats.ToolOutputBytes)
	observed := estSys + estTools + estConv + estToolOut
	if observed <= 0 {
		return nil
	}

	out := &proto.ContextBreakdown{Source: proto.ContextBreakdownEstimated}
	if fullWindow && observed <= contextTokens {
		// All transcript turns since session start or last compaction were observed.
		// The unrecorded prefix (system prompt, rules, and engine-injected context) makes up the gap.
		sysTotal := estSys + (contextTokens - observed)
		if sysTotal > 0 || estSys > 0 {
			out.SystemAndRules = proto.Int(sysTotal)
		}
		if estTools > 0 {
			out.ToolsAndMCP = proto.Int(estTools)
		}
		out.Conversation = proto.Int(estConv)
		out.ToolOutputs = proto.Int(estToolOut)
	} else {
		// Either the byte heuristic exceeded actual contextTokens or we only scanned a bounded tail:
		// proportionally apportion actual contextTokens across the observed categories without
		// fabricating unobserved categories.
		type bucket struct {
			weight  int
			present bool
			alloc   int
			rem     int64
		}
		buckets := [4]bucket{
			{weight: estSys, present: estSys > 0},
			{weight: estTools, present: estTools > 0},
			{weight: estConv, present: estConv > 0 || fullWindow},
			{weight: estToolOut, present: estToolOut > 0 || fullWindow},
		}
		totalWeight := int64(observed)
		allocated := 0
		for i := range buckets {
			if buckets[i].weight <= 0 {
				continue
			}
			num := int64(buckets[i].weight) * int64(contextTokens)
			buckets[i].alloc = int(num / totalWeight)
			buckets[i].rem = num % totalWeight
			allocated += buckets[i].alloc
		}
		// Distribute rounding remainder deterministically by largest remainder (tie-break by index).
		left := contextTokens - allocated
		for left > 0 {
			best := -1
			for i := range buckets {
				if buckets[i].weight <= 0 {
					continue
				}
				if best < 0 || buckets[i].rem > buckets[best].rem {
					best = i
				}
			}
			if best < 0 {
				break
			}
			buckets[best].alloc++
			buckets[best].rem = -1
			left--
		}
		if buckets[0].present {
			out.SystemAndRules = proto.Int(buckets[0].alloc)
		}
		if buckets[1].present {
			out.ToolsAndMCP = proto.Int(buckets[1].alloc)
		}
		if buckets[2].present {
			out.Conversation = proto.Int(buckets[2].alloc)
		}
		if buckets[3].present {
			out.ToolOutputs = proto.Int(buckets[3].alloc)
		}
	}

	if contextWindow > 0 {
		out.Free = proto.Int(max(contextWindow-contextTokens, 0))
	}
	if autoCompactThreshold > 0 {
		out.AutoCompactThreshold = proto.Int(autoCompactThreshold)
	}
	if !out.Valid() {
		return nil
	}
	return out
}
