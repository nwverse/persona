package transcript

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// Questions a Codex agent asks the user, followed through its rollout (docs/integrations/codex.md "Questions and approvals"):
//
//   - request_user_input_async (Default mode, models whose catalog lists it): the call returns {"accepted":true} at
//     once and the turn goes on or ends; the item_completed AgentMessage with delivery "async" carries the questions
//     (title, options as strings) — written only for a call Codex accepted. A user message with a <send_user_message_question_reply> answers them by
//     questionItemId ["request_user_input_async", <call id>, <index>]; any other user message is the user moving on.
//   - request_user_input (Plan mode): the turn waits on the TUI's overlay; the questions are the call's arguments
//     (id, header, question, options with descriptions) and its function_call_output is the answer.
//
// Both close when the turn is aborted.

// CodexAsk is a question call nobody has answered yet.
type CodexAsk struct {
	CallID string
	// Async: request_user_input_async, answered by a reply message; else request_user_input's overlay.
	Async     bool
	Questions []CodexAskQuestion
	At        time.Time
}

// CodexAskQuestion is one question of a call.
type CodexAskQuestion struct {
	Index    int    // its place in the call (a reply names it by that)
	ID       string // request_user_input's answer key ("color"); "" for async
	Header   string
	Question string
	Options  []proto.QuestionOption
}

// CodexAsks follows the open question calls of one rollout, a line at a time.
type CodexAsks struct {
	open []CodexAsk
}

// Open lists the calls still waiting on an answer, oldest first.
func (a *CodexAsks) Open() []CodexAsk {
	out := make([]CodexAsk, len(a.open))
	copy(out, a.open)
	return out
}

var (
	// Lines worth decoding: everything else (the bulk of a rollout) is skipped on a byte search.
	askMarks = [][]byte{[]byte("request_user_input"), []byte(`"delivery":"async"`), []byte(`"UserMessage"`), []byte("turn_aborted")}
	// CodexReplyRe is the reply envelope Codex's clients put an answer in (the desktop app, the 0.163 TUI).
	CodexReplyRe = lazyre.New(`(?s)<send_user_message_question_reply>\s*(.*?)\s*</send_user_message_question_reply>`)
)

// Feed reads the next rollout line.
func (a *CodexAsks) Feed(line []byte) {
	interesting := false
	for _, m := range askMarks {
		if bytes.Contains(line, m) {
			interesting = true
			break
		}
	}
	if !interesting {
		for _, o := range a.open {
			if bytes.Contains(line, []byte(o.CallID)) { // its function_call_output
				interesting = true
				break
			}
		}
	}
	if !interesting {
		return
	}
	var l struct {
		Timestamp time.Time `json:"timestamp"`
		Type      string    `json:"type"`
		Payload   struct {
			Type      string                    `json:"type"`
			Name      string                    `json:"name"`
			Arguments string                    `json:"arguments"`
			CallID    string                    `json:"call_id"`
			Item      *askItem                  `json:"item"`
			Display   []struct{ Item *askItem } `json:"display_completions"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil {
		return
	}
	p := l.Payload
	switch {
	case l.Type == "response_item" && p.Type == "function_call" && p.Name == "request_user_input":
		a.add(p.CallID, false, []byte(p.Arguments), l.Timestamp)
	// request_user_input_async's own function_call is not enough: a call Codex rejects (arguments it cannot parse, which
	// the model then retries) is followed by an error, not by the async item that carries the questions.
	case l.Type == "response_item" && (p.Type == "function_call_output" || p.Type == "custom_tool_call_output"):
		accepted := bytes.Contains(line, []byte(`\"accepted\":true`))
		a.close(func(o CodexAsk) bool { return o.CallID == p.CallID && (!o.Async || !accepted) })
	case l.Type == "event_msg" && p.Type == "turn_aborted":
		a.open = nil
	case l.Type == "event_msg" && p.Type == "item_completed" && p.Item != nil:
		a.item(p.Item, l.Timestamp)
	case l.Type == "history_mutation": // a compatible CLI: the items arrive as display completions
		for _, d := range p.Display {
			if d.Item != nil {
				a.item(d.Item, l.Timestamp)
			}
		}
	}
}

type askItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Delivery  string          `json:"delivery"`
	Questions json.RawMessage `json:"questions"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (a *CodexAsks) item(it *askItem, at time.Time) {
	switch it.Type {
	case "AgentMessage":
		if it.Delivery == "async" && len(it.Questions) > 0 {
			a.add(it.ID, true, []byte(`{"questions":`+string(it.Questions)+`}`), at)
		}
	case "UserMessage":
		var text strings.Builder
		for _, c := range it.Content {
			text.WriteString(c.Text)
		}
		a.reply(text.String())
	}
}

// add opens a call from its arguments (either schema); a call seen before (the function_call and its async item
// both carry it) is kept as first read.
func (a *CodexAsks) add(callID string, async bool, args []byte, at time.Time) {
	if callID == "" {
		return
	}
	for _, o := range a.open {
		if o.CallID == callID {
			return
		}
	}
	var v struct {
		Questions []struct {
			ID       string            `json:"id"`
			Header   string            `json:"header"`
			Question string            `json:"question"`
			Title    string            `json:"title"`
			Options  []json.RawMessage `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(args, &v) != nil || len(v.Questions) == 0 {
		return
	}
	ask := CodexAsk{CallID: callID, Async: async, At: at}
	for i, q := range v.Questions {
		qq := CodexAskQuestion{Index: i, ID: q.ID, Header: q.Header, Question: strings.TrimSpace(q.Question)}
		if qq.Question == "" {
			qq.Question = strings.TrimSpace(q.Title)
		}
		for _, o := range q.Options {
			var label string
			var opt proto.QuestionOption
			if json.Unmarshal(o, &label) == nil {
				opt.Label = label
			} else if json.Unmarshal(o, &opt) != nil {
				continue
			}
			if strings.TrimSpace(opt.Label) != "" {
				qq.Options = append(qq.Options, opt)
			}
		}
		if qq.Question != "" {
			ask.Questions = append(ask.Questions, qq)
		}
	}
	if len(ask.Questions) > 0 {
		a.open = append(a.open, ask)
	}
}

// reply takes a user message: the questions its envelope answers close (a call whose questions are all answered
// closes); a message without one is the user moving on, which closes every async call.
func (a *CodexAsks) reply(text string) {
	blocks := CodexReplyRe.FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		a.close(func(o CodexAsk) bool { return o.Async })
		return
	}
	for _, b := range blocks {
		for _, r := range codexReplies(b[1]) {
			call, index := ParseQuestionItemID(r.QuestionItemID)
			for i := range a.open {
				o := &a.open[i]
				if !o.Async || o.CallID != call {
					continue
				}
				kept := o.Questions[:0:0]
				for _, q := range o.Questions {
					if index >= 0 && q.Index != index {
						kept = append(kept, q)
					}
				}
				o.Questions = kept
			}
		}
	}
	a.close(func(o CodexAsk) bool { return len(o.Questions) == 0 })
}

func (a *CodexAsks) close(match func(CodexAsk) bool) {
	kept := a.open[:0]
	for _, o := range a.open {
		if !match(o) {
			kept = append(kept, o)
		}
	}
	a.open = kept
}

// CodexReply is one answer in a reply envelope.
type CodexReply struct {
	QuestionItemID string `json:"questionItemId"`
	Question       string `json:"question"`
	Answer         string `json:"answer"`
}

// codexReplies reads an envelope's JSON: a list, or one reply.
func codexReplies(s string) []CodexReply {
	var many []CodexReply
	if json.Unmarshal([]byte(s), &many) == nil {
		return many
	}
	var one CodexReply
	if json.Unmarshal([]byte(s), &one) == nil && one.QuestionItemID != "" {
		return []CodexReply{one}
	}
	return nil
}

// QuestionItemID is how a reply names an async question: the JSON text of ["request_user_input_async", call, index].
func QuestionItemID(call string, index int) string {
	b, _ := json.Marshal([]any{"request_user_input_async", call, index})
	return string(b)
}

// ParseQuestionItemID reads a reply's questionItemId: the call and the question's index, or -1 for a reply that
// names the whole call (older desktop replies name the item).
func ParseQuestionItemID(id string) (call string, index int) {
	var parts []any
	if json.Unmarshal([]byte(id), &parts) == nil && len(parts) == 3 {
		c, _ := parts[1].(string)
		if f, ok := parts[2].(float64); ok {
			return c, int(f)
		}
		return c, -1
	}
	return id, -1
}

// CodexReplyMessage is the user message that answers async questions: one envelope on one line (a newline typed into
// the TUI would submit early), its JSON not HTML-escaped (the model reads it as is).
func CodexReplyMessage(replies []CodexReply) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(replies)
	return "<send_user_message_question_reply>" + strings.TrimSpace(b.String()) + "</send_user_message_question_reply>"
}
