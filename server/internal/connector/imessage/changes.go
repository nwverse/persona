package imessage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// What goes to Persona' knowledge base, inbox and assistant (the user's "Hand to Persona" switch, on the hub): new
// messages by ROWID, and a search. Only real conversations go (Keep), and codes are masked (MaskCodes); the app's
// own view of a conversation (Messages) shows everything as it is.

// Batch is one conversation's new messages in a Changes page, oldest first.
type Batch struct {
	Chat     Chat      `json:"chat"`
	Messages []Message `json:"messages"`
}

// ChangesPage is what came in after a cursor (a message ROWID), and the cursor to go on from.
type ChangesPage struct {
	Batches []Batch `json:"batches"`
	Next    string  `json:"next"`
	More    bool    `json:"more,omitempty"`
}

// Keep modes: which conversations go to Persona.
const (
	// KeepContacts: one where someone is in this Mac's contacts, or the user has written: what a person would call a
	// conversation, without the codes, banks and parcels that come from numbers nobody saved.
	KeepContacts = "contacts"
	KeepAll      = "all"
)

// Changes is what came in after cursor, at most limit messages (tapbacks left out), in the conversations keep lets
// through, codes masked. The first time (cursor "") it starts at since. Next moves past everything read, kept or not.
func (r *Reader) Changes(ctx context.Context, cursor string, limit int, since time.Time, keep string) (ChangesPage, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var start int64
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n < 0 {
			return ChangesPage{}, fmt.Errorf("bad cursor")
		}
		start = n
	} else {
		// Just before the oldest message dated since then. ROWIDs follow arrival, not dates: an old message iCloud
		// brings in late has a high ROWID (and is after the start, which is right: it is new here).
		var rows []struct{ N int64 }
		err := r.query(ctx, r.chatDB(), fmt.Sprintf(`SELECT coalesce(min(ROWID), (SELECT coalesce(max(ROWID), 0) + 1 FROM message)) - 1 AS n
FROM message WHERE date >= %d`, appleNanos(since)), &rows)
		if err != nil {
			return ChangesPage{}, err
		}
		if len(rows) == 1 {
			start = rows[0].N
		}
	}
	var rows []msgRow
	if err := r.query(ctx, r.chatDB(), fmt.Sprintf(`SELECT j.chat_id AS chat, %s FROM message m JOIN chat_message_join j ON j.message_id = m.ROWID
WHERE m.ROWID > %d AND m.associated_message_type = 0 ORDER BY m.ROWID LIMIT %d`, msgColumns, start, limit+1), &rows); err != nil {
		return ChangesPage{}, err
	}
	page := ChangesPage{Batches: []Batch{}, Next: strconv.FormatInt(start, 10)}
	if len(rows) > limit {
		rows, page.More = rows[:limit], true
	}
	if len(rows) == 0 {
		return page, nil
	}
	page.Next = strconv.FormatInt(rows[len(rows)-1].RowID, 10)

	convs, err := r.conversations(ctx)
	if err != nil {
		return ChangesPage{}, err
	}
	byChat := map[int64]*conversation{}
	for _, cv := range convs {
		for _, id := range cv.chats {
			byChat[id] = cv
		}
	}
	var order []*conversation
	grouped := map[string][]msgRow{}
	var ids, withFiles []int64
	var threads []string
	for _, m := range rows {
		cv := byChat[m.ChatRow]
		if cv == nil {
			continue
		}
		if _, ok := grouped[cv.id]; !ok {
			order = append(order, cv)
		}
		grouped[cv.id] = append(grouped[cv.id], m)
		ids = append(ids, m.Handle, m.Other)
		if m.Attachments != 0 {
			withFiles = append(withFiles, m.RowID)
		}
		if t := str(m.Thread); t != "" {
			threads = append(threads, t)
		}
	}
	if len(order) == 0 {
		return page, nil
	}
	people, err := r.members(ctx, order)
	if err != nil {
		return ChangesPage{}, err
	}
	names := r.names(ctx)
	wrote := map[string]bool{}
	if keep != KeepAll {
		if wrote, err = r.wrote(ctx, order); err != nil {
			return ChangesPage{}, err
		}
	}
	handles, err := r.handles(ctx, ids)
	if err != nil {
		return ChangesPage{}, err
	}
	files, err := r.attachments(ctx, withFiles)
	if err != nil {
		return ChangesPage{}, err
	}
	replied, err := r.replied(ctx, threads)
	if err != nil {
		return ChangesPage{}, err
	}
	for _, cv := range order {
		chat := r.chat(cv, people[cv.id], names)
		if keep != KeepAll && !wrote[cv.id] && !anyNamed(chat.Members) {
			continue
		}
		b := Batch{Chat: chat}
		for _, m := range grouped[cv.id] {
			msg := message(m, names, handles, files[m.RowID])
			if t := str(m.Thread); t != "" {
				msg.ReplyTo = MaskCodes(replied[t])
			}
			msg.Text = MaskCodes(msg.Text)
			b.Messages = append(b.Messages, msg)
		}
		page.Batches = append(page.Batches, b)
	}
	return page, nil
}

func anyNamed(people []Person) bool {
	for _, p := range people {
		if p.Name != "" {
			return true
		}
	}
	return false
}

// wrote says which conversations the user has written in, ever.
func (r *Reader) wrote(ctx context.Context, convs []*conversation) (map[string]bool, error) {
	var chats []int64
	owner := map[int64]string{}
	for _, cv := range convs {
		for _, id := range cv.chats {
			chats = append(chats, id)
			owner[id] = cv.id
		}
	}
	var rows []struct {
		Chat int64 `json:"chat"`
	}
	if err := r.query(ctx, r.chatDB(), `SELECT DISTINCT j.chat_id AS chat FROM chat_message_join j JOIN message m ON m.ROWID = j.message_id
WHERE j.chat_id IN (`+ints(chats)+`) AND m.is_from_me = 1`, &rows); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, row := range rows {
		out[owner[row.Chat]] = true
	}
	return out, nil
}

// appleNanos is t as Messages dates it: nanoseconds since 2001.
func appleNanos(t time.Time) int64 { return (t.Unix() - appleEpoch) * 1e9 }

var (
	codeWords = lazyre.New(`(?i)验证码|校验码|动态码|确认码|动态密码|安全码|激活码|\bcode\b|\botp\b|passcode|\bpin\b`)
	codeDigit = lazyre.New(`\b\d{4,8}\b`)
)

// MaskCodes hides the digits of a message that is about a code (verification code 123456 → verification code ••••): what Persona keeps
// and gives to models does not carry one-time codes.
func MaskCodes(s string) string {
	if !codeWords.MatchString(s) {
		return s
	}
	return codeDigit.ReplaceAllString(s, "••••")
}

// Hit is a message a search found, with its conversation.
type Hit struct {
	Chat    Chat    `json:"chat"`
	Message Message `json:"message"`
}

// searchScan is how many of the newest messages a search reads: the texts only in attributedBody have to be decoded
// to be matched, so the matching is here, not in SQL (where the query would have to be spliced in as text).
const searchScan = 20000

// Search finds the newest messages since then whose text contains query (any case), in the conversations keep lets
// through, at most limit, codes masked.
func (r *Reader) Search(ctx context.Context, query string, limit int, since time.Time, keep string) ([]Hit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows []msgRow
	if err := r.query(ctx, r.chatDB(), fmt.Sprintf(`SELECT j.chat_id AS chat, %s FROM message m JOIN chat_message_join j ON j.message_id = m.ROWID
WHERE m.date >= %d AND m.associated_message_type = 0 AND m.item_type = 0 ORDER BY m.date DESC LIMIT %d`, msgColumns, appleNanos(since), searchScan), &rows); err != nil {
		return nil, err
	}
	var found []msgRow
	for _, m := range rows {
		if text, ok := m.text(); ok && strings.Contains(strings.ToLower(text), q) {
			found = append(found, m)
		}
	}
	if len(found) == 0 {
		return []Hit{}, nil
	}
	convs, err := r.conversations(ctx)
	if err != nil {
		return nil, err
	}
	byChat := map[int64]*conversation{}
	for _, cv := range convs {
		for _, id := range cv.chats {
			byChat[id] = cv
		}
	}
	var hitConvs []*conversation
	seen := map[string]bool{}
	var ids []int64
	for _, m := range found {
		if cv := byChat[m.ChatRow]; cv != nil && !seen[cv.id] {
			seen[cv.id] = true
			hitConvs = append(hitConvs, cv)
		}
		ids = append(ids, m.Handle)
	}
	people, err := r.members(ctx, hitConvs)
	if err != nil {
		return nil, err
	}
	names := r.names(ctx)
	wrote := map[string]bool{}
	if keep != KeepAll {
		if wrote, err = r.wrote(ctx, hitConvs); err != nil {
			return nil, err
		}
	}
	handles, err := r.handles(ctx, ids)
	if err != nil {
		return nil, err
	}
	chats := map[string]Chat{}
	for _, cv := range hitConvs {
		chats[cv.id] = r.chat(cv, people[cv.id], names)
	}
	hits := []Hit{}
	for _, m := range found {
		cv := byChat[m.ChatRow]
		if cv == nil || keep != KeepAll && !wrote[cv.id] && !anyNamed(chats[cv.id].Members) {
			continue
		}
		msg := message(m, names, handles, nil)
		msg.Text = MaskCodes(msg.Text)
		hits = append(hits, Hit{Chat: chats[cv.id], Message: msg})
		if len(hits) == limit {
			break
		}
	}
	return hits, nil
}
