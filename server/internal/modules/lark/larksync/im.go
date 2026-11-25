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
	chatsPagesMost     = 20              // 2000 chats
	chatPagesMost      = 40              // 2000 messages of one chat per run; the rest next run (ascending order)
	threadPagesMost    = 10              // 500 replies of one thread per run
	searchPagesMost    = 10              // 500 general search hits per run
	p2pSearchPagesMost = 40              // 2000 p2p search hits per run for single-chat discovery
	p2pChatsMost       = 2000            // remembered single chats per binding
	messageOverlap     = 5 * time.Minute // re-read before a chat's cursor: messages that landed late
	searchResource     = larksyncwire.ResSearch
	p2pSearchResource  = larksyncwire.ResSearch + ":p2p"
	chatResourceStem   = larksyncwire.ResIM + ":"
	p2pChatsResource   = chatResourceStem + "p2p_chats"
	// searchChatTypeP2P is the chat_type filter for POST /open-apis/search/v2/message to match single (p2p) chats only.
	// Feishu Open Platform docs: https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/search-v2/message/create
	// (oapi-sdk-go/v3/service/search/v2 CreateMessageReqBody.ChatType: "group_chat" | "p2p_chat").
	searchChatTypeP2P = "p2p_chat"
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

// readIM reads every chat the owner is in, one resource per chat ("im:<chat_id>"), plus every previously discovered
// single (p2p) chat remembered in p2pChatsResource: GET /open-apis/im/v1/chats lists only group chats for a user, so
// single chats are discovered separately through p2p message search and then read incrementally by chat_id just like
// group chats. The documents tier does not read chats at all.
func (r *run) readIM(ctx context.Context) error {
	if r.b.Tier != larksyncwire.TierFull {
		return nil
	}
	r.loadP2PChats()
	defer r.saveP2PChats()
	chats, err := r.listChats(ctx)
	if err != nil {
		return r.settle(ctx, larksyncwire.ResIM, err)
	}
	for _, c := range chats {
		if r.overBudget() {
			r.partial = true
			return nil
		}
		if c.kind == "p2p" {
			if c.name == "" && r.p2pChats[c.id] != "" {
				c.name = r.p2pChats[c.id]
				r.chats[c.id] = c
			}
			r.recordP2PChat(c.id, c.name)
		}
		if err := r.readChat(ctx, c); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(r.p2pChats) {
		if r.readChats[id] {
			continue
		}
		if r.overBudget() {
			r.partial = true
			return nil
		}
		ci := chatInfo{id: id, name: r.p2pChats[id], kind: "p2p"}
		r.chats[id] = ci
		if err := r.readChat(ctx, ci); err != nil {
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
	r.readChats[c.id] = true
	resource := chatResourceStem + c.id
	from := r.backfillFrom().Unix()
	old := parseUnix(r.cursors[resource])
	if old > 0 {
		from = old/1000 - int64(messageOverlap/time.Second)
	}
	end := r.start.Unix()
	seen := map[string]bool{}
	var msgs []larksyncwire.Message
	extraURLs := map[string][]string{}
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
			if urls := extraMessageURLs(am); len(urls) > 0 {
				extraURLs[am.MessageID] = urls
			}
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
	if c.kind == "p2p" {
		peer := c.name
		if peer == "" {
			for _, m := range msgs {
				if m.ChatName != "" {
					peer = m.ChatName
					break
				}
			}
		}
		if peer == "" && len(msgs) > 0 {
			if peer = r.fetchP2PChatName(ctx, c.id); peer != "" {
				for i := range msgs {
					if msgs[i].ChatName == "" {
						msgs[i].ChatName = peer
					}
				}
			}
		}
		if peer != "" && peer != c.name {
			c.name = peer
			r.chats[c.id] = c
		}
		r.recordP2PChat(c.id, c.name)
	}
	for _, m := range msgs {
		r.addMessage(resource, m, larksyncwire.EntryChat)
		r.findMessageLeads(resource, larksyncwire.EntryMessage, m.Text)
		for _, u := range extraURLs[m.ID] {
			r.findMessageLeads(resource, larksyncwire.EntryMessage, u)
		}
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

// readP2PDiscovery runs only the p2p chat discovery step (used by ModeIM incremental syncs to pick up newly started
// single chats without running the broader unfiltered message search).
func (r *run) readP2PDiscovery(ctx context.Context) error {
	_, err := r.discoverP2PChats(ctx)
	return err
}

// readSearch first discovers single (p2p) chats via chat_type=p2p_chat search and backfills each discovered chat's
// full history through GET /open-apis/im/v1/messages (so active group chats cannot crowd p2p chats out of the search
// results), then runs the general message search for anything else the chat list does not reach (such as a chat the
// owner left). Best effort: any failure fails only "search".
func (r *run) readSearch(ctx context.Context) error {
	if r.b.Tier != larksyncwire.TierFull || r.overBudget() {
		return nil
	}
	ok, err := r.discoverP2PChats(ctx)
	if err != nil || !ok || r.overBudget() {
		return err
	}
	from := r.backfillFrom().Unix()
	if old := parseUnix(r.cursors[searchResource]); old > 0 {
		from = old - int64(messageOverlap/time.Second)
	}
	end := r.start.Unix()
	var found []larksyncwire.Message
	extraURLs := map[string][]string{}
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
			m, urls, ok, err := r.fetchMessageWithURLs(ctx, id)
			if err != nil {
				return r.settle(ctx, searchResource, err)
			}
			if ok {
				found = append(found, m)
				if len(urls) > 0 {
					extraURLs[m.ID] = urls
				}
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
		r.findMessageLeads(searchResource, larksyncwire.EntryMessage, m.Text)
		for _, u := range extraURLs[m.ID] {
			r.findMessageLeads(searchResource, larksyncwire.EntryMessage, u)
		}
	}
	r.moveCursor(searchResource, unixCursor(end))
	return nil
}

// discoverP2PChats searches POST /open-apis/search/v2/message filtered to chat_type="p2p_chat" across all pages in
// [from, end] to find single chats that GET /open-apis/im/v1/chats omits for user tokens. Each newly discovered chat
// is immediately backfilled via readChat (im/v1/messages) and remembered in p2pChatsResource so subsequent hits from
// the same chat skip fetchRawMessage and future runs sync it incrementally without relying on search.
// ok is true when discovery finished normally and general readSearch may proceed.
func (r *run) discoverP2PChats(ctx context.Context) (bool, error) {
	if r.b.Tier != larksyncwire.TierFull || r.overBudget() {
		return false, nil
	}
	defer r.saveP2PChats()
	from := r.backfillFrom().Unix()
	if old := parseUnix(r.cursors[p2pSearchResource]); old > 0 {
		from = old - int64(messageOverlap/time.Second)
	}
	end := r.start.Unix()
	seen := map[string]bool{}
	tok := ""
	for range p2pSearchPagesMost {
		if r.overBudget() {
			r.abandon(p2pSearchResource)
			return false, nil
		}
		var data struct {
			page
			Items []string `json:"items"`
		}
		q := map[string]string{"page_size": "50", "user_id_type": "open_id"}
		if tok != "" {
			q["page_token"] = tok
		}
		body := map[string]string{
			"query":      "",
			"chat_type":  searchChatTypeP2P,
			"start_time": strconv.FormatInt(from, 10),
			"end_time":   strconv.FormatInt(end, 10),
		}
		err := r.sess.getJSON(ctx, request{family: famSearch, op: "search p2p messages", method: "POST", path: "/open-apis/search/v2/message", query: q, body: body}, &data)
		if err != nil {
			return false, r.settle(ctx, searchResource, err)
		}
		for _, id := range data.Items {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if _, ok := r.msgs[larksyncwire.KeyMessage+id]; ok {
				continue // already read through readChat in this run
			}
			if r.overBudget() {
				r.abandon(p2pSearchResource)
				return false, nil
			}
			am, ok, err := r.fetchRawMessage(ctx, id)
			if err != nil {
				return false, r.settle(ctx, searchResource, err)
			}
			if !ok || am.ChatID == "" {
				continue
			}
			if existing, known := r.chats[am.ChatID]; known && existing.kind != "" && existing.kind != "p2p" {
				continue // never reclassify a group/topic chat returned by listChats
			}
			r.recordP2PChat(am.ChatID, r.p2pChats[am.ChatID])
			if !r.readChats[am.ChatID] {
				if r.overBudget() {
					r.abandon(p2pSearchResource)
					return false, nil
				}
				ci := chatInfo{id: am.ChatID, name: r.p2pChats[am.ChatID], kind: "p2p"}
				r.chats[am.ChatID] = ci
				if err := r.readChat(ctx, ci); err != nil {
					return false, err
				}
			}
		}
		if !data.HasMore || data.PageToken == "" {
			break
		}
		tok = data.PageToken
	}
	r.moveCursor(p2pSearchResource, unixCursor(end))
	return true, nil
}

// fetchRawMessage reads one API message by id. ok is false for one that cannot be read (deleted, or no longer visible
// to the owner): it is skipped, not a failure.
func (r *run) fetchRawMessage(ctx context.Context, id string) (apiMessage, bool, error) {
	var data struct {
		Items []apiMessage `json:"items"`
	}
	err := r.sess.getJSON(ctx, request{family: famIM, op: "get message", path: "/open-apis/im/v1/messages/" + pathEsc(id), query: map[string]string{"user_id_type": "open_id"}}, &data)
	if err != nil {
		if e := asSyncError(err); e != nil && e.Kind == larksyncwire.ErrKindOther {
			return apiMessage{}, false, nil
		}
		return apiMessage{}, false, err
	}
	for _, am := range data.Items {
		if am.MessageID == id {
			return am, true, nil
		}
	}
	return apiMessage{}, false, nil
}

// fetchMessageWithURLs reads one message by id and any URLs embedded in interactive card action fields. ok is false
// for one that cannot be read (deleted, or no longer visible to the owner): it is skipped, not a failure.
func (r *run) fetchMessageWithURLs(ctx context.Context, id string) (larksyncwire.Message, []string, bool, error) {
	am, ok, err := r.fetchRawMessage(ctx, id)
	if err != nil || !ok || am.Deleted {
		return larksyncwire.Message{}, nil, false, err
	}
	c := r.chats[am.ChatID]
	if c.id == "" {
		c = chatInfo{id: am.ChatID}
	}
	return r.message(am, c), extraMessageURLs(am), true, nil
}

// extraMessageURLs extracts URLs stored in interactive card action/link fields ("url", "href", "pc_url", "android_url",
// "ios_url") that cardText omits when flattening plain text.
func extraMessageURLs(am apiMessage) []string {
	if am.MsgType != "interactive" || strings.TrimSpace(am.Body.Content) == "" {
		return nil
	}
	var raw any
	if err := json.Unmarshal([]byte(am.Body.Content), &raw); err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range sortedKeys(x) {
				switch k {
				case "url", "href", "pc_url", "android_url", "ios_url":
					if s, ok := x[k].(string); ok && strings.HasPrefix(strings.TrimSpace(s), "http") {
						out = append(out, strings.TrimSpace(s))
					}
				default:
					walk(x[k])
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(raw)
	return out
}

// loadP2PChats loads the remembered single chats (chat_id -> peer name) from cursors. Invalid or empty cursor data is
// ignored so a run never fails on metadata.
func (r *run) loadP2PChats() {
	raw := strings.TrimSpace(r.cursors[p2pChatsResource])
	if raw == "" {
		return
	}
	var saved map[string]string
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return
	}
	for id, name := range saved {
		if id != "" {
			r.recordP2PChat(id, name)
		}
	}
}

func (r *run) recordP2PChat(id, name string) {
	if id == "" {
		return
	}
	if old, ok := r.p2pChats[id]; ok {
		if name != "" && old != name {
			r.p2pChats[id] = name
		}
		return
	}
	if len(r.p2pChats) >= p2pChatsMost {
		return
	}
	r.p2pChats[id] = name
}

func (r *run) saveP2PChats() {
	if len(r.p2pChats) == 0 {
		return
	}
	raw, err := json.Marshal(r.p2pChats)
	if err != nil {
		return
	}
	if s := string(raw); s != r.cursors[p2pChatsResource] {
		r.moveCursor(p2pChatsResource, s)
	}
}

// fetchP2PChatName tries GET /open-apis/im/v1/chats/:chat_id as a best-effort fallback when a single chat's messages
// in this window did not include a named human peer (for example, only the owner sent messages, or the peer is a bot).
// Feishu Open Platform docs: https://open.feishu.cn/document/server-docs/group/chat/get
func (r *run) fetchP2PChatName(ctx context.Context, chatID string) string {
	if chatID == "" || r.chatMetaTried[chatID] || ctx.Err() != nil {
		return ""
	}
	r.chatMetaTried[chatID] = true
	var data struct {
		Name          string `json:"name"`
		OwnerID       string `json:"owner_id"`
		P2PTargetID   string `json:"p2p_target_id"`
		P2PTargetType string `json:"p2p_target_type"`
	}
	err := r.sess.getJSON(ctx, request{family: famIM, op: "chat info", path: "/open-apis/im/v1/chats/" + pathEsc(chatID),
		query: map[string]string{"user_id_type": "open_id"}}, &data)
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(data.Name); name != "" {
		return name
	}
	if data.P2PTargetID != "" && isContactUser(data.P2PTargetID, data.P2PTargetType) {
		if name := r.senderName(ctx, data.P2PTargetID); name != "" {
			return name
		}
	}
	if data.OwnerID != "" && data.OwnerID != r.owner && isContactUser(data.OwnerID, "") {
		return r.senderName(ctx, data.OwnerID)
	}
	return ""
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
