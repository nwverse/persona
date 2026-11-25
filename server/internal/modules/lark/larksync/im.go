package larksync

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// IM reading bounds.
const (
	chatsPagesMost   = 20              // 2000 chats
	chatPagesMost    = 40              // 2000 messages of one chat per run; the rest next run (ascending order)
	threadPagesMost  = 10              // 500 replies of one thread per run
	searchPagesMost  = 10              // 500 search hits per run
	messageOverlap   = 5 * time.Minute // re-read before a chat's cursor: messages that landed late
	searchResource   = larksyncwire.ResSearch
	chatResourceStem = larksyncwire.ResIM + ":"
)

// apiChat is one chat of GET /open-apis/im/v1/chats (as the user: the chats they are in).
type apiChat struct {
	ChatID   string `json:"chat_id"`
	Name     string `json:"name"`
	ChatMode string `json:"chat_mode"` // p2p | group | topic, when the answer carries it
}

// apiMessage is one message of the im/v1 message APIs.
type apiMessage struct {
	MessageID  string `json:"message_id"`
	ThreadID   string `json:"thread_id"`
	MsgType    string `json:"msg_type"`
	CreateTime string `json:"create_time"` // milliseconds
	Deleted    bool   `json:"deleted"`
	ChatID     string `json:"chat_id"`
	Sender     struct {
		ID         string `json:"id"`
		SenderType string `json:"sender_type"`
	} `json:"sender"`
	Body struct {
		Content string `json:"content"`
	} `json:"body"`
	Mentions []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"mentions"`
}

// page is the paging part of a list answer.
type page struct {
	HasMore   bool   `json:"has_more"`
	PageToken string `json:"page_token"`
}

// readIM reads every chat the owner is in, one resource per chat ("im:<chat_id>"). The documents tier does not read
// chats at all.
func (r *run) readIM(ctx context.Context) error {
	if r.b.Tier != larksyncwire.TierFull {
		return nil
	}
	chats, err := r.listChats(ctx)
	if err != nil {
		return r.settle(ctx, larksyncwire.ResIM, err)
	}
	for _, c := range chats {
		if r.overBudget() {
			r.partial = true
			return nil
		}
		if err := r.readChat(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) listChats(ctx context.Context) ([]chatInfo, error) {
	var out []chatInfo
	tok := ""
	for range chatsPagesMost {
		var data struct {
			page
			Items []apiChat `json:"items"`
		}
		q := map[string]string{"page_size": "100", "user_id_type": "open_id"}
		if tok != "" {
			q["page_token"] = tok
		}
		if err := r.sess.getJSON(ctx, request{family: famIM, op: "list chats", path: "/open-apis/im/v1/chats", query: q}, &data); err != nil {
			return nil, err
		}
		for _, c := range data.Items {
			if c.ChatID == "" {
				continue
			}
			ci := chatInfo{id: c.ChatID, name: c.Name, kind: c.ChatMode}
			if ci.kind == "" {
				ci.kind = "group"
			}
			r.chats[c.ChatID] = ci
			out = append(out, ci)
		}
		if !data.HasMore || data.PageToken == "" {
			break
		}
		tok = data.PageToken
	}
	return out, nil
}

// readChat reads one chat's messages since its cursor (less the overlap) and the replies of the threads they belong
// to, in the same window. Nothing of the chat counts unless all of it was read.
func (r *run) readChat(ctx context.Context, c chatInfo) error {
	resource := chatResourceStem + c.id
	from := r.backfillFrom().Unix()
	old := parseUnix(r.cursors[resource])
	if old > 0 {
		from = old/1000 - int64(messageOverlap/time.Second)
	}
	end := r.start.Unix()
	seen := map[string]bool{}
	var msgs []larksyncwire.Message
	var threads []string
	var last int64 // the newest create_time read from the chat itself, in ms
	capped := false
	add := func(items []apiMessage) {
		for _, am := range items {
			if am.MessageID == "" || seen[am.MessageID] || am.Deleted {
				continue
			}
			seen[am.MessageID] = true
			m := r.message(am, c)
			msgs = append(msgs, m)
			if am.ThreadID != "" && !containsString(threads, am.ThreadID) {
				threads = append(threads, am.ThreadID)
			}
		}
	}
	tok := ""
	for n := 0; ; n++ {
		if n == chatPagesMost {
			capped = true
			break
		}
		if r.overBudget() {
			r.abandon(resource)
			return nil
		}
		items, more, next, err := r.listMessages(ctx, "chat", c.id, from, end, tok)
		if err != nil {
			return r.settle(ctx, resource, err)
		}
		for _, am := range items {
			last = max(last, parseUnix(am.CreateTime))
		}
		add(items)
		if !more || next == "" {
			break
		}
		tok = next
	}
	for _, th := range threads {
		tok := ""
		for range threadPagesMost {
			if r.overBudget() {
				r.abandon(resource)
				return nil
			}
			items, more, next, err := r.listMessages(ctx, "thread", th, from, end, tok)
			if err != nil {
				return r.settle(ctx, resource, err)
			}
			for _, am := range items {
				if am.ChatID == "" {
					am.ChatID = c.id
				}
			}
			add(items)
			if !more || next == "" {
				break
			}
			tok = next
		}
	}
	r.nameSenders(ctx, msgs)
	for _, m := range msgs {
		r.addMessage(resource, m, larksyncwire.EntryChat)
		r.findLeads(resource, larksyncwire.EntryMessage, m.Text)
	}
	cursor := max(last, old)
	if !capped && cursor == 0 {
		cursor = end * 1000 // a quiet chat: the next run need not look back the whole backfill again
	}
	r.moveCursor(resource, unixCursor(cursor))
	return nil
}

// listMessages is one page of a chat's or a thread's messages in [from, end] (seconds), oldest first.
func (r *run) listMessages(ctx context.Context, containerType, id string, from, end int64, pageToken string) ([]apiMessage, bool, string, error) {
	var data struct {
		page
		Items []apiMessage `json:"items"`
	}
	q := map[string]string{
		"container_id_type": containerType,
		"container_id":      id,
		"start_time":        strconv.FormatInt(from, 10),
		"end_time":          strconv.FormatInt(end, 10),
		"sort_type":         "ByCreateTimeAsc",
		"page_size":         "50",
		"user_id_type":      "open_id",
	}
	if pageToken != "" {
		q["page_token"] = pageToken
	}
	err := r.sess.getJSON(ctx, request{family: famIM, op: "list messages", path: "/open-apis/im/v1/messages", query: q}, &data)
	return data.Items, data.HasMore, data.PageToken, err
}

// message turns an API message of chat c into the sync's Message.
func (r *run) message(am apiMessage, c chatInfo) larksyncwire.Message {
	if am.Sender.ID != "" && am.Sender.SenderType != "" {
		r.senderTypes[am.Sender.ID] = am.Sender.SenderType
	}
	text, files := messageText(am)
	for i := range files {
		files[i].Ref = attachmentRef(r.b.Key, am.MessageID, files[i].Ref, files[i].Kind)
	}
	chatID := am.ChatID
	if chatID == "" {
		chatID = c.id
	}
	return larksyncwire.Message{
		ID:       am.MessageID,
		ChatID:   chatID,
		ChatName: c.name,
		ChatType: c.kind,
		ThreadID: am.ThreadID,
		SenderID: am.Sender.ID,
		Mine:     r.owner != "" && am.Sender.ID == r.owner,
		Type:     am.MsgType,
		Text:     text,
		At:       time.UnixMilli(parseUnix(am.CreateTime)).UTC(),
		Files:    files,
	}
}

// readSearch reads the owner's messages found by message search since its cursor: what the chat list does not
// reach (a chat the owner left, a single chat). Best effort: any failure fails only "search". The search API with an
// empty query is not documented to list everything; when it answers nothing, nothing is lost.
func (r *run) readSearch(ctx context.Context) error {
	if r.b.Tier != larksyncwire.TierFull || r.overBudget() {
		return nil
	}
	from := r.backfillFrom().Unix()
	if old := parseUnix(r.cursors[searchResource]); old > 0 {
		from = old - int64(messageOverlap/time.Second)
	}
	end := r.start.Unix()
	var found []larksyncwire.Message
	var again []string // hits already read through a chat: only the search source is added
	tok := ""
	for range searchPagesMost {
		if r.overBudget() {
			r.abandon(searchResource)
			return nil
		}
		var data struct {
			page
			Items []string `json:"items"`
		}
		q := map[string]string{"page_size": "50", "user_id_type": "open_id"}
		if tok != "" {
			q["page_token"] = tok
		}
		body := map[string]string{"query": "", "start_time": strconv.FormatInt(from, 10), "end_time": strconv.FormatInt(end, 10)}
		err := r.sess.getJSON(ctx, request{family: famSearch, op: "search messages", method: "POST", path: "/open-apis/search/v2/message", query: q, body: body}, &data)
		if err != nil {
			return r.settle(ctx, searchResource, err)
		}
		for _, id := range data.Items {
			if id == "" {
				continue
			}
			if _, ok := r.msgs[larksyncwire.KeyMessage+id]; ok {
				if !containsString(again, id) {
					again = append(again, id)
				}
				continue
			}
			if containsMessage(found, id) {
				continue
			}
			m, ok, err := r.fetchMessage(ctx, id)
			if err != nil {
				return r.settle(ctx, searchResource, err)
			}
			if ok {
				found = append(found, m)
			}
		}
		if !data.HasMore || data.PageToken == "" {
			break
		}
		tok = data.PageToken
	}
	for _, id := range again {
		st := r.msgs[larksyncwire.KeyMessage+id]
		if st != nil {
			r.addMessage(searchResource, st.msg, larksyncwire.EntrySearch)
		}
	}
	r.nameSenders(ctx, found)
	for _, m := range found {
		r.addMessage(searchResource, m, larksyncwire.EntrySearch)
		r.findLeads(searchResource, larksyncwire.EntryMessage, m.Text)
	}
	r.moveCursor(searchResource, unixCursor(end))
	return nil
}

// fetchMessage reads one message by id. ok is false for one that cannot be read (deleted, or no longer visible to
// the owner): it is skipped, not a failure.
func (r *run) fetchMessage(ctx context.Context, id string) (larksyncwire.Message, bool, error) {
	var data struct {
		Items []apiMessage `json:"items"`
	}
	err := r.sess.getJSON(ctx, request{family: famIM, op: "get message", path: "/open-apis/im/v1/messages/" + pathEsc(id), query: map[string]string{"user_id_type": "open_id"}}, &data)
	if err != nil {
		if e := asSyncError(err); e != nil && e.Kind == larksyncwire.ErrKindOther {
			return larksyncwire.Message{}, false, nil
		}
		return larksyncwire.Message{}, false, err
	}
	for _, am := range data.Items {
		if am.MessageID != id || am.Deleted {
			continue
		}
		c := r.chats[am.ChatID]
		if c.id == "" {
			c = chatInfo{id: am.ChatID}
		}
		return r.message(am, c), true, nil
	}
	return larksyncwire.Message{}, false, nil
}

// messageText is a message's plain text by its type, and its attachments (Ref holds the bare file or image key; the
// caller makes it a full Ref).
func messageText(am apiMessage) (string, []larksyncwire.Attachment) {
	var c map[string]any
	_ = json.Unmarshal([]byte(am.Body.Content), &c)
	str := func(k string) string { s, _ := c[k].(string); return s }
	switch am.MsgType {
	case "text":
		text := str("text")
		for _, m := range am.Mentions {
			if m.Key != "" {
				text = strings.ReplaceAll(text, m.Key, "@"+m.Name)
			}
		}
		return text, nil
	case "post":
		return postText(c)
	case "image":
		return "[image]", attachment(str("image_key"), "", "image")
	case "file":
		return "[file " + str("file_name") + "]", attachment(str("file_key"), str("file_name"), "file")
	case "media":
		return "[file " + str("file_name") + "]", attachment(str("file_key"), str("file_name"), "file")
	case "audio":
		return "[audio]", attachment(str("file_key"), "", "file")
	case "interactive":
		return cardText(c), nil
	}
	if am.MsgType == "" {
		return "[message]", nil
	}
	return "[" + am.MsgType + "]", nil
}

func attachment(key, name, kind string) []larksyncwire.Attachment {
	if key == "" {
		return nil
	}
	return []larksyncwire.Attachment{{Ref: key, Name: name, Kind: kind}}
}

// postText flattens a rich-text post: its title, then each paragraph's text, links and mentions; its images and
// videos become attachments. A post read back may be wrapped in a language ("zh_cn": {…}).
func postText(c map[string]any) (string, []larksyncwire.Attachment) {
	if _, ok := c["content"]; !ok {
		for _, k := range sortedKeys(c) {
			if inner, ok := c[k].(map[string]any); ok {
				if _, ok := inner["content"]; ok {
					c = inner
					break
				}
			}
		}
	}
	var lines []string
	var files []larksyncwire.Attachment
	if t, _ := c["title"].(string); strings.TrimSpace(t) != "" {
		lines = append(lines, t)
	}
	paras, _ := c["content"].([]any)
	for _, p := range paras {
		elems, _ := p.([]any)
		var b strings.Builder
		for _, e := range elems {
			el, _ := e.(map[string]any)
			s := func(k string) string { v, _ := el[k].(string); return v }
			switch s("tag") {
			case "text", "md":
				b.WriteString(s("text"))
			case "a":
				if s("href") != "" && s("href") != s("text") {
					b.WriteString(s("text") + " (" + s("href") + ")")
				} else {
					b.WriteString(cmpString(s("text"), s("href")))
				}
			case "at":
				b.WriteString("@" + cmpString(s("user_name"), s("user_id")))
			case "img":
				b.WriteString("[image]")
				files = append(files, attachment(s("image_key"), "", "image")...)
			case "media":
				b.WriteString("[video]")
				files = append(files, attachment(s("file_key"), "", "file")...)
			case "code_block":
				b.WriteString("\n" + s("text") + "\n")
			case "emotion":
				b.WriteString(":" + s("emoji_type") + ":")
			case "hr":
				b.WriteString("---")
			}
		}
		if line := strings.TrimSpace(b.String()); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), files
}

// cardText is an interactive card's title, then its plain texts in order.
func cardText(c map[string]any) string {
	var parts []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range []string{"title", "text", "content"} {
				if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
				}
			}
			keys := sortedKeys(x)
			// The header (the card's title) comes first, whatever its key sorts as.
			sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "header" && keys[j] != "header" })
			for _, k := range keys {
				if _, isText := x[k].(string); !isText {
					walk(x[k])
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(c)
	if len(parts) == 0 {
		return "[card]"
	}
	return strings.Join(parts, "\n")
}

func cmpString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsMessage(list []larksyncwire.Message, id string) bool {
	for _, m := range list {
		if m.ID == id {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nameSenders fills in the senders' display names, and a single chat's name when the platform gives none (the other
// person's name, as the Lark gateway names it): the knowledge base matches a message it already has from the gateway
// by chat name, sender and text (kb.NewGatewayAligner), and the gateway writes names, not open ids. Best effort:
// non-user senders (bots/apps such as cli_* or sender_type != "user") are skipped without querying the contact API;
// a missing contact scope stops lookups for the rest of the run, while a single sender's failure leaves only that
// sender unnamed.
func (r *run) nameSenders(ctx context.Context, msgs []larksyncwire.Message) {
	for i := range msgs {
		m := &msgs[i]
		if m.SenderName == "" && isContactUser(m.SenderID, r.senderTypes[m.SenderID]) {
			m.SenderName = r.senderName(ctx, m.SenderID)
		}
	}
	peers := map[string]string{}
	for _, m := range msgs {
		if m.ChatType == "p2p" && m.ChatName == "" && !m.Mine && m.SenderName != "" {
			if _, ok := peers[m.ChatID]; !ok {
				peers[m.ChatID] = m.SenderName
			}
		}
	}
	if len(peers) == 0 {
		return
	}
	for i := range msgs {
		if msgs[i].ChatName == "" {
			if peer := peers[msgs[i].ChatID]; peer != "" {
				msgs[i].ChatName = peer
			}
		}
	}
}

// isContactUser reports whether a sender can be looked up through contact/v3/users by open_id.
// Bot/app senders (sender_type "app" or cli_* app ids) and other non-user senders ("anonymous", "unknown") are not
// contact users; querying contact/v3/users for them always fails.
func isContactUser(senderID, senderType string) bool {
	if senderID == "" {
		return false
	}
	if st := strings.ToLower(strings.TrimSpace(senderType)); st != "" && st != "user" {
		return false
	}
	return !strings.HasPrefix(senderID, "cli_")
}

// senderName is one open id's display name, read once per run.
func (r *run) senderName(ctx context.Context, openID string) string {
	if name, ok := r.names[openID]; ok {
		return name
	}
	if r.namesOff || ctx.Err() != nil {
		return ""
	}
	var data struct {
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	err := r.sess.getJSON(ctx, request{family: famIM, op: "user name", path: "/open-apis/contact/v3/users/" + pathEsc(openID),
		query: map[string]string{"user_id_type": "open_id"}}, &data)
	if err != nil {
		if e := asSyncError(err); e != nil && e.Kind == larksyncwire.ErrKindScope {
			r.namesOff = true
			return ""
		}
		// Only a missing contact scope disables lookups for the rest of the run; a per-sender failure
		// (deleted/external user, invalid id, transient or rate-limit error) leaves only this sender unnamed
		// and is cached for the run so repeated messages from them do not repeat the failed lookup.
		if ctx.Err() == nil {
			r.names[openID] = ""
		}
		return ""
	}
	r.names[openID] = data.User.Name
	return data.User.Name
}
