package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// BotMux structured turn endpoint contract (deepcoldy/botmux#3597, #3599):
// GET /api/sessions/:sessionId/turns?since=<cursor>&limit=<n> returns BotmuxTurnsResponse.
const (
	BotmuxTurnsSchema   = "botmux.session-turns.v1"
	BotmuxCapOwnerActor = "ownerActor"
)

// BotmuxTurnsResponse is the JSON payload of BotMux's read-only GET /api/sessions/:id/turns endpoint.
type BotmuxTurnsResponse struct {
	OK           bool         `json:"ok"`
	Schema       string       `json:"schema"`
	Capabilities []string     `json:"capabilities"`
	Next         string       `json:"next"`
	Turns        []BotmuxTurn `json:"turns"`
}

// BotmuxTurn is one structured turn record from BotMux.
type BotmuxTurn struct {
	TurnID  string              `json:"turnId"`
	Seq     int64               `json:"seq"`
	At      int64               `json:"at"`
	Inbound []BotmuxTurnInbound `json:"inbound"`
	Replies []BotmuxTurnReply   `json:"replies"`
	Anchor  *BotmuxTurnAnchor   `json:"anchor,omitempty"`
}

// BotmuxTurnInbound is one inbound message folded into a BotMux turn.
type BotmuxTurnInbound struct {
	MessageID       string                 `json:"messageId"`
	Text            string                 `json:"text"`
	Sender          *BotmuxTurnSender      `json:"sender,omitempty"`
	Via             string                 `json:"via"` // "lark" | "trigger" | "owner_actor"
	QuotedMessageID string                 `json:"quotedMessageId,omitempty"`
	Attachments     []BotmuxTurnAttachment `json:"attachments,omitempty"`
	Mentions        []BotmuxTurnMention    `json:"mentions,omitempty"`
	Placement       *BotmuxTurnPlacement   `json:"placement,omitempty"`
}

// BotmuxTurnSender identifies the sender in BotMux's record. OpenID is parsed only so it can be ignored;
// only display names leave the node.
type BotmuxTurnSender struct {
	Type   string `json:"type"`
	OpenID string `json:"openId,omitempty"`
	Name   string `json:"name,omitempty"`
}

// BotmuxTurnAttachment is safe attachment metadata in BotMux's record.
type BotmuxTurnAttachment struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// BotmuxTurnMention is a mentioned user in BotMux's record. OpenID is never forwarded.
type BotmuxTurnMention struct {
	OpenID string `json:"openId,omitempty"`
	Name   string `json:"name,omitempty"`
}

// BotmuxTurnPlacement describes whether the message landed in a topic ("thread") or directly in the chat ("chat").
type BotmuxTurnPlacement struct {
	Kind          string `json:"kind"` // "thread" | "chat"
	RootMessageID string `json:"rootMessageId,omitempty"`
}

// BotmuxTurnReply is one confirmed reply posted by BotMux during the turn.
type BotmuxTurnReply struct {
	MessageID    string `json:"messageId"`
	ResponseKind string `json:"responseKind"`
	At           int64  `json:"at"`
	Text         string `json:"text,omitempty"`
}

// BotmuxTurnAnchor links a BotMux turn to the CLI transcript entry it was delivered to.
type BotmuxTurnAnchor struct {
	CLIID        string `json:"cliId"`
	CLISessionID string `json:"cliSessionId,omitempty"`
	InputSha256  string `json:"inputSha256"`
	CLIEntryID   string `json:"cliEntryId,omitempty"`
}

// HasCapability reports whether the turns response advertises cap.
func (r BotmuxTurnsResponse) HasCapability(cap string) bool {
	return r.OK && r.Schema == BotmuxTurnsSchema && slices.Contains(r.Capabilities, cap)
}

// ApplyBotmuxTurns overlays structured BotMux turn records onto transcript items.
// Each user turn is matched by anchor.cliEntryId first, then anchor.inputSha256.
// Matched turns populate Item.Botmux for the user message and for the turn's `botmux send` replies
// (including file/card replies whose text was not on the command line). Unmatched turns are left
// untouched so wrapper parsing from markBotmux acts as a per-turn fallback.
// Item.Text is never modified, and no open_id or messageId ever leaves the node.
func ApplyBotmuxTurns(items []proto.Item, turns []BotmuxTurn, transcriptSessionID string) []proto.Item {
	if len(items) == 0 || len(turns) == 0 {
		return items
	}
	byEntry := map[string]int{}
	byHash := map[string][]int{}
	for i := range turns {
		a := turns[i].Anchor
		if a == nil {
			continue
		}
		if id := strings.TrimSpace(a.CLISessionID); id != "" && id != transcriptSessionID {
			continue
		}
		if id := strings.TrimSpace(a.CLIEntryID); id != "" {
			byEntry[id] = i
		} else if h := strings.ToLower(strings.TrimSpace(a.InputSha256)); h != "" {
			byHash[h] = append(byHash[h], i)
		}
	}
	if len(byEntry) == 0 && len(byHash) == 0 {
		return items
	}

	used := make([]bool, len(turns))
	var out []proto.Item
	clone := func() []proto.Item {
		if out == nil {
			out = append([]proto.Item(nil), items...)
		}
		return out
	}

	// Index tool results by ToolUseID so reply matching only considers completed, successful calls.
	resultsByToolUse := map[string]*proto.Item{}
	for i := range items {
		if items[i].Kind == proto.KindToolResult && items[i].ToolUseID != "" {
			resultsByToolUse[items[i].ToolUseID] = &items[i]
		}
	}

	// Find all user text item indices (turn boundaries).
	var userIdx []int
	for i := range items {
		it := &items[i]
		if it.Role == "user" && it.Kind == proto.KindText && it.From == "" {
			userIdx = append(userIdx, i)
		}
	}

	matchTurn := func(it proto.Item) int {
		if it.ID != "" {
			if idx, ok := byEntry[it.ID]; ok && !used[idx] {
				return idx
			}
			if prefix, _, found := strings.Cut(it.ID, "#"); found && prefix != "" {
				if idx, ok := byEntry[prefix]; ok && !used[idx] {
					return idx
				}
			}
		}
		for _, candidate := range itemInputHashCandidates(it) {
			if idxs := byHash[candidate]; len(idxs) == 1 && !used[idxs[0]] {
				return idxs[0]
			}
		}
		return -1
	}

	for ui, start := range userIdx {
		tIdx := matchTurn(items[start])
		if tIdx < 0 {
			continue // per-turn fallback: keep wrapper-parsed Botmux as is
		}
		used[tIdx] = true
		turn := turns[tIdx]

		end := len(items)
		if ui+1 < len(userIdx) {
			end = userIdx[ui+1]
		}

		if bm := botmuxFromInbound(turn.Inbound, items[start].Botmux); bm != nil {
			dst := clone()
			dst[start].Botmux = bm
		}

		if len(turn.Replies) == 0 {
			continue
		}
		repliesByID := make(map[string]BotmuxTurnReply, len(turn.Replies))
		for _, reply := range turn.Replies {
			if reply.MessageID != "" {
				repliesByID[reply.MessageID] = reply
			}
		}
		for j := start + 1; j < end; j++ {
			it := &items[j]
			if it.Kind != proto.KindToolUse {
				continue
			}
			result := resultsByToolUse[it.ToolUseID]
			if !botmuxToolResultSucceeded(result) {
				continue
			}
			sendCount := countBotmuxSendInvocations(botmuxCommand(it.ToolName, it.ToolInput))
			if sendCount == 0 {
				continue
			}
			messageIDs := botmuxSendReceiptIDs(result.Text)
			if len(messageIDs) > sendCount {
				continue
			}
			var replies []BotmuxTurnReply
			for _, messageID := range messageIDs {
				if reply, ok := repliesByID[messageID]; ok {
					replies = append(replies, reply)
					delete(repliesByID, messageID)
				}
			}
			if bm := botmuxFromReplies(replies, it.Botmux); bm != nil {
				dst := clone()
				dst[j].Botmux = bm
			}
		}
	}

	if out == nil {
		return items
	}
	return out
}

func botmuxSendReceiptIDs(text string) []string {
	var messageIDs []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var receipt struct {
			Success   bool   `json:"success"`
			MessageID string `json:"messageId"`
		}
		if json.Unmarshal([]byte(line), &receipt) == nil && receipt.Success && receipt.MessageID != "" {
			messageIDs = append(messageIDs, receipt.MessageID)
		}
	}
	return messageIDs
}

func itemInputHashCandidates(it proto.Item) []string {
	if it.RawTextSHA256 != "" {
		if it.RawTextTrimmedSHA256 != "" && it.RawTextTrimmedSHA256 != it.RawTextSHA256 {
			return []string{it.RawTextSHA256, it.RawTextTrimmedSHA256}
		}
		return []string{it.RawTextSHA256}
	}
	return inputHashCandidates(it.Text)
}

func inputHashCandidates(text string) []string {
	if text == "" {
		return nil
	}
	h1 := sha256Hex(text)
	trimmed := strings.TrimRight(text, "\r\n")
	if trimmed == text {
		return []string{h1}
	}
	return []string{h1, sha256Hex(trimmed)}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func botmuxFromInbound(inbound []BotmuxTurnInbound, existing *proto.Botmux) *proto.Botmux {
	if len(inbound) == 0 {
		return nil
	}
	b := &proto.Botmux{Kind: proto.BotmuxMessage}
	allTrigger := true
	var parts []string
	var mentions []string
	var meta []proto.BotmuxAttachment
	for _, m := range inbound {
		if m.Via != "trigger" {
			allTrigger = false
		}
		if t := strings.TrimSpace(m.Text); t != "" {
			parts = append(parts, t)
		}
		if b.Sender == "" && m.Sender != nil {
			// Display name only: never fall back to openId.
			if name := strings.TrimSpace(m.Sender.Name); name != "" {
				b.Sender = name
				b.SenderType = strings.TrimSpace(m.Sender.Type)
			}
		}
		if strings.TrimSpace(m.QuotedMessageID) != "" {
			b.Quoted = true
		}
		if m.Placement != nil && m.Placement.Kind == "thread" {
			b.Thread = true
		}
		for _, a := range m.Attachments {
			name := strings.TrimSpace(a.Name)
			if name != "" {
				name = path.Base(strings.ReplaceAll(name, "\\", "/"))
				if name == "." || name == "/" {
					name = ""
				}
			}
			meta = append(meta, proto.BotmuxAttachment{
				Kind:     strings.TrimSpace(a.Kind),
				Name:     name,
				MimeType: strings.TrimSpace(a.MimeType),
			})
		}
		for _, mn := range m.Mentions {
			// Display name only: never include openId.
			if name := strings.TrimSpace(mn.Name); name != "" && !slices.Contains(mentions, name) {
				mentions = append(mentions, name)
			}
		}
	}
	if allTrigger {
		b.Kind = proto.BotmuxTrigger
		if existing != nil && existing.Source != "" {
			b.Source = existing.Source
		}
	}
	if existing != nil && existing.TopicContext {
		b.TopicContext = true
	}
	b.Text = strings.Join(parts, "\n\n")
	if len(meta) > 0 {
		b.Attachments = len(meta)
		b.AttachmentMeta = meta
	}
	if len(mentions) > 0 {
		b.Mentions = mentions
	}
	if b.Text == "" && b.Attachments == 0 && !b.Quoted {
		return nil
	}
	return b
}

func botmuxFromReply(r BotmuxTurnReply, existing *proto.Botmux) *proto.Botmux {
	text := strings.TrimSpace(r.Text)
	if text == "" && existing != nil {
		text = existing.Text
	}
	final := r.ResponseKind == "final"
	if r.ResponseKind == "" && existing != nil {
		final = existing.Final
	}
	var voice bool
	var attachments int
	if existing != nil {
		voice = existing.Voice
		attachments = existing.Attachments
	}
	if text == "" && !voice && attachments == 0 {
		return nil
	}
	return &proto.Botmux{
		Kind:        proto.BotmuxReply,
		Text:        text,
		Final:       final,
		Voice:       voice,
		Attachments: attachments,
	}
}

func botmuxFromReplies(replies []BotmuxTurnReply, existing *proto.Botmux) *proto.Botmux {
	if len(replies) == 0 {
		return nil
	}
	if len(replies) == 1 {
		return botmuxFromReply(replies[0], existing)
	}
	var parts []string
	final := false
	for _, r := range replies {
		if t := strings.TrimSpace(r.Text); t != "" {
			parts = append(parts, t)
		}
		if r.ResponseKind == "final" {
			final = true
		}
	}
	text := strings.Join(parts, "\n\n")
	if text == "" && existing != nil {
		text = existing.Text
	}
	var voice bool
	var attachments int
	if existing != nil {
		voice = existing.Voice
		attachments = existing.Attachments
	}
	if text == "" && !voice && attachments == 0 {
		return nil
	}
	return &proto.Botmux{
		Kind:        proto.BotmuxReply,
		Text:        text,
		Final:       final,
		Voice:       voice,
		Attachments: attachments,
	}
}

// countBotmuxSendInvocations counts syntactically valid `botmux send` invocations in cmd
// (excluding --help, -h, --dry-run, unknown flags, and invalid flag syntax), including forms whose
// body is not on the command line (--content-file, --card-file, --card-json, heredocs, pipes).
// Heredoc bodies are skipped so lines inside a message are not miscounted as separate commands.
func countBotmuxSendInvocations(cmd string) int {
	if !strings.Contains(cmd, "botmux") || !strings.Contains(cmd, "send") {
		return 0
	}
	count := 0
	for _, seg := range splitShellSegments(cmd) {
		words := splitShellWordsSimple(seg)
		// Skip leading KEY=VAL environment assignments.
		for len(words) > 0 && isEnvAssign(words[0]) {
			words = words[1:]
		}
		if len(words) < 2 || path.Base(words[0]) != "botmux" || words[1] != "send" {
			continue
		}
		if isValidBotmuxSendArgs(words[2:]) {
			count++
		}
	}
	return count
}

func isValidBotmuxSendArgs(words []string) bool {
	// Strip shell redirections (e.g. 2>&1, >/dev/null, <<'EOF') before checking flags.
	cleaned := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == ">" || w == ">>" || w == "1>" || w == "2>" || w == "&>" || w == "<<" || w == "<<-" {
			i++ // skip redirection target / delimiter
			continue
		}
		if strings.HasPrefix(w, ">") || strings.HasPrefix(w, "1>") || strings.HasPrefix(w, "2>") ||
			strings.HasPrefix(w, "&>") || strings.HasPrefix(w, "<<") {
			continue
		}
		cleaned = append(cleaned, w)
	}
	for i := 0; i < len(cleaned); i++ {
		w := cleaned[i]
		if w == "-h" || w == "--" {
			return false
		}
		if !strings.HasPrefix(w, "--") {
			continue
		}
		name, value, eq := strings.Cut(w, "=")
		switch botmuxSendFlags[name] {
		case sendFlagBool:
			if eq {
				return false
			}
		case sendFlagOptional:
		case sendFlagValue, sendFlagFileValue:
			if !eq {
				if i+1 >= len(cleaned) || cleaned[i+1] == "-h" || strings.HasPrefix(cleaned[i+1], "--") {
					return false
				}
				i++
				value = cleaned[i]
			}
			if value == "" {
				return false
			}
		default: // sendFlagGiveUp (--help, --dry-run) or unknown flag
			return false
		}
	}
	return true
}

// splitShellSegments splits a shell command on unquoted ;, &&, ||, |, and newlines,
// skipping heredoc bodies so lines inside a heredoc are not treated as separate commands.
func splitShellSegments(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	var pendingDelim string
	var pendingStrip bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(s) {
				cur.WriteByte(c)
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '\\':
			cur.WriteByte(c)
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
		case '<':
			if pendingDelim == "" && strings.HasPrefix(s[i:], "<<") && !strings.HasPrefix(s[i:], "<<<") {
				cur.WriteString("<<")
				i += 2
				strip := false
				if i < len(s) && s[i] == '-' {
					strip = true
					cur.WriteByte('-')
					i++
				}
				for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
					cur.WriteByte(s[i])
					i++
				}
				j := i
				for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' && s[j] != ';' && s[j] != '|' && s[j] != '&' {
					j++
				}
				if d, _, ok := parseHeredocDelim(s[i:j]); ok {
					pendingDelim = d
					pendingStrip = strip
				}
				cur.WriteString(s[i:j])
				i = j - 1
				continue
			}
			cur.WriteByte(c)
		case '\n':
			if seg := strings.TrimSpace(cur.String()); seg != "" {
				out = append(out, seg)
			}
			cur.Reset()
			if pendingDelim != "" {
				// Skip heredoc body lines until the matching delimiter line.
				rest := s[i+1:]
				pos := 0
				for pos < len(rest) {
					nl := strings.IndexByte(rest[pos:], '\n')
					var line string
					var nextPos int
					if nl < 0 {
						line = rest[pos:]
						nextPos = len(rest)
					} else {
						line = rest[pos : pos+nl]
						nextPos = pos + nl + 1
					}
					checkLine := line
					if pendingStrip {
						checkLine = strings.TrimLeft(checkLine, "\t")
					}
					pos = nextPos
					if checkLine == pendingDelim {
						break
					}
				}
				i += pos
				pendingDelim = ""
				pendingStrip = false
			}
		case ';':
			if seg := strings.TrimSpace(cur.String()); seg != "" {
				out = append(out, seg)
			}
			cur.Reset()
		case '&', '|':
			if c == '&' && ((i > 0 && s[i-1] == '>') || (i+1 < len(s) && s[i+1] == '>')) {
				cur.WriteByte(c)
				continue
			}
			if i+1 < len(s) && s[i+1] == c {
				i++
			}
			if seg := strings.TrimSpace(cur.String()); seg != "" {
				out = append(out, seg)
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if seg := strings.TrimSpace(cur.String()); seg != "" {
		out = append(out, seg)
	}
	return out
}

func splitShellWordsSimple(s string) []string {
	var words []string
	var cur strings.Builder
	var quote byte
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			inWord = true
		case '\\':
			inWord = true
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			}
		case ' ', '\t', '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

func isEnvAssign(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := w[i]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
