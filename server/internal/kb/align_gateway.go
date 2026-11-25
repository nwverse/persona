package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/managerconf"
)

// Aligning the Lark user sync's messages with the Lark gateway's.
//
// The gateway (source "lark") archives the owner's Lark chats as "lark-<feed id>", by ids this repository cannot map to
// the open platform's oc_/om_ ids; the user sync archives the same chats as "larksync-<oc_ id>". A chat file belongs to
// one connector (attributeChat), so the sync cannot append into the gateway's chats: instead every message it hands
// over is looked for in the gateway's archive first, and one found there is not written again (Engine.Align).
//
// A message is the same when its chat, its sender, its body and its time agree (messageFingerprint, the same rule as
// the sync's larksync.MessageFingerprint): the chat is the one the crosswalk pairs it with (L1), or else any gateway
// chat of the same name (L2), whose day files are read for the message's day; the times agree when their 2-second
// buckets are at most one apart. The day files keep only the minute; the gateway chat's receipts give a line its exact
// time when they bind it unambiguously (receiptLineHash), and a line without one agrees when its minute holds the
// message's time. The first L2 match pairs the two chats in the crosswalk, so later messages compare within that
// chat alone.

// crosswalkFile pairs the sync's chats with the gateway's: <data>/chats/.crosswalk.json, {"<sync chat id>": "<gateway
// chat id>"}, both knowledge-base chat ids ("larksync-oc_…" → "lark-…").
const crosswalkFile = ".crosswalk.json"

// alignNamesEvery is how long the gateway chats' names are kept before they are listed again: a chat the gateway
// creates meanwhile is found within it.
const alignNamesEvery = time.Minute

// alignDaysMost bounds the parsed day files kept between messages (a batch is mostly one chat's one or two days).
const alignDaysMost = 64

// alignMidnight: a message this close to midnight is also looked for in the neighbouring day's file (a chat's day
// files may have been written in another zone, and the times may differ by a bucket across midnight).
const alignMidnight = 10 * time.Minute

// NewGatewayAligner is an Engine.Align for the Lark user sync (larksyncwire.ConnectorID): it says a message is a
// duplicate when the Lark gateway already archived it, and pairs the two chats in the crosswalk on the first match.
// Messages of every other source are never duplicates.
func NewGatewayAligner(s *Store) func(Source, SourceChat, SourceMessage) bool {
	a := &gatewayAligner{s: s, now: time.Now, days: map[string]gatewayDay{}}
	return a.align
}

// gatewayAligner keeps what one aligner read: the crosswalk, the gateway chats by name and recently parsed days.
type gatewayAligner struct {
	s   *Store
	now func() time.Time

	mu       sync.Mutex
	walk     map[string]string // the crosswalk; nil until read
	names    map[string][]string
	namesAt  time.Time
	days     map[string]gatewayDay // "<chat id>/<day>" → its parsed lines
	walkFail bool                  // the crosswalk could not be read: L2 only, and nothing is written over it
}

// gatewayDay is one gateway day file's message lines, kept while the file is unchanged.
type gatewayDay struct {
	mod   time.Time
	size  int64
	lines []gatewayLine
}

// gatewayLine is one archived message as the aligner compares it.
type gatewayLine struct {
	minute time.Time // the line's minute (the day file's "15:04")
	exact  time.Time // its time from the chat's receipts; zero when they do not bind it to one
	sender string
	body   string // without its trailing attachments
	media  bool   // it had attachments
}

func (a *gatewayAligner) align(src Source, sc SourceChat, m SourceMessage) bool {
	if !syncSource(src) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := ChatID(src.Name(), sc.ID)
	walk := a.crosswalk()
	if gw, ok := walk[id]; ok {
		return a.within(gw, sc, m) // L1: only the paired chat
	}
	name := normalizeText(sc.Name)
	if name == "" {
		return false
	}
	for _, gw := range a.gatewayChats()[name] {
		if a.within(gw, sc, m) {
			a.pair(id, gw)
			return true
		}
	}
	return false
}

// syncSource: src is the Lark user sync (its archive source or its connector).
func syncSource(src Source) bool {
	if src.Name() == larksyncwire.ConnectorID {
		return true
	}
	if o, ok := src.(interface{ ConnectorID() string }); ok {
		return o.ConnectorID() == larksyncwire.ConnectorID
	}
	return false
}

// ownerSender is how archive lines (buildLine) and syncMessage name the owner's own messages.
const ownerSender = "我" // i18n-ignore: archive data

// unknownSender is how archive lines (buildLine) and syncMessage mark a message whose sender has no display name.
const unknownSender = "?"

// within reports whether gateway chat gw archived m.
func (a *gatewayAligner) within(gw string, sc SourceChat, m SourceMessage) bool {
	sender, body, media := syncMessage(m)
	senderUnknown := sender == unknownSender
	want := map[string]bool{}
	for k := -1; k <= 1; k++ {
		want[messageFingerprint(sc.Name, sender, m.At.Add(time.Duration(k)*2*time.Second), body)] = true
	}
	same := messageFingerprint(sc.Name, sender, m.At, body)
	var wantNoSender map[string]bool
	var sameNoSender string
	ensureNoSender := func() {
		if wantNoSender != nil {
			return
		}
		wantNoSender = make(map[string]bool, 3)
		for k := -1; k <= 1; k++ {
			wantNoSender[messageFingerprint(sc.Name, "", m.At.Add(time.Duration(k)*2*time.Second), body)] = true
		}
		sameNoSender = messageFingerprint(sc.Name, "", m.At, body)
	}
	for _, day := range alignDays(m.At, a.s.Zone()) {
		for _, l := range a.day(gw, day) {
			if l.body == "" && body == "" && (!l.media || !media) {
				continue // two empty texts are the same message only when both were attachments
			}
			// When one side has no display name (for example a bot/app sender or a binding without contact scope)
			// and the other side is not the owner's own message, degrade to the senderless fingerprint
			// (chat name + 2-second bucket + body) so the message still aligns with the gateway's copy.
			fallbackSender := (senderUnknown && l.sender != ownerSender) || (l.sender == unknownSender && sender != ownerSender)
			if !l.exact.IsZero() {
				if want[messageFingerprint(sc.Name, l.sender, l.exact, l.body)] {
					return true
				}
				if fallbackSender {
					ensureNoSender()
					if wantNoSender[messageFingerprint(sc.Name, "", l.exact, l.body)] {
						return true
					}
				}
				continue
			}
			// Only the minute: the line agrees when its minute, widened by a bucket each side, holds the message.
			if m.At.Before(l.minute.Add(-2*time.Second)) || !m.At.Before(l.minute.Add(time.Minute+2*time.Second)) {
				continue
			}
			if messageFingerprint(sc.Name, l.sender, m.At, l.body) == same {
				return true
			}
			// On a minute-only line (no exact timestamp), require a non-empty body before ignoring the sender so
			// two unnamed attachment-only messages in the same minute do not collide.
			if fallbackSender && body != "" {
				ensureNoSender()
				if messageFingerprint(sc.Name, "", m.At, l.body) == sameNoSender {
					return true
				}
			}
		}
	}
	return false
}

// syncPlaceholder is the text the sync gives an image or a file of a message ("[image]", "[file <name>]"), which the
// gateway's archive writes as an attachment instead.
var syncPlaceholder = lazyre.New(`\s*\[(?:image|file(?: [^\]]*)?)\]\s*$`)

// syncMessage is m's sender as an archive line names it (buildLine: the owner is "我") and its text without the
// attachments the line would carry.
func syncMessage(m SourceMessage) (sender, body string, media bool) {
	sender = strings.TrimSpace(m.SenderName)
	if m.Mine {
		sender = ownerSender
	}
	if sender == "" {
		sender = unknownSender
	}
	body = strings.TrimSpace(m.Text)
	media = len(m.Media) > 0
	if media {
		for {
			loc := syncPlaceholder.FindStringIndex(body)
			if loc == nil {
				break
			}
			body = body[:loc[0]]
		}
	}
	return sender, body, media
}

// alignDays are the days (YYYY-MM-DD in zone) whose files may hold a message of at.
func alignDays(at time.Time, zone *time.Location) []string {
	t := at.In(zone)
	out := []string{t.Format("2006-01-02")}
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, zone)
	if t.Sub(start) < alignMidnight {
		out = append(out, start.Add(-time.Hour).Format("2006-01-02"))
	}
	if next := start.AddDate(0, 0, 1); next.Sub(t) <= alignMidnight {
		out = append(out, next.Format("2006-01-02"))
	}
	return out
}

// day is gateway chat id's lines of day, parsed again only when its file changed.
func (a *gatewayAligner) day(id, day string) []gatewayLine {
	p := a.s.path(KindChat, id, "messages", day+".md")
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	key := id + "/" + day
	if d, ok := a.days[key]; ok && d.mod.Equal(st.ModTime()) && d.size == st.Size() {
		return d.lines
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	zone := a.s.Zone()
	// The exact times: the receipts bound to a line by its minute and its rendered text (as DayWithIdentity does).
	var r messageReceipts
	_, _ = readJSON(a.s.path(KindChat, id, ingestReceipts), &r)
	exact := map[string][]time.Time{}
	for ref, line := range r.Lines {
		at, ok := r.IDs[ref]
		if !ok || line.Hash == "" || at.In(zone).Format("2006-01-02") != day {
			continue
		}
		k := at.In(zone).Format("15:04 ") + line.Hash
		exact[k] = append(exact[k], at)
	}
	var lines []gatewayLine
	for _, msg := range messages(string(raw)) {
		t := msg.text
		if len(t) < 6 || t[5] != ' ' {
			continue
		}
		minute, err := time.ParseInLocation("2006-01-02 15:04", day+" "+t[:5], zone)
		if err != nil {
			continue
		}
		sender, text, ok := strings.Cut(t[6:], ": ")
		if !ok {
			continue
		}
		l := gatewayLine{minute: minute, sender: sender}
		if ts := exact[t[:6]+receiptBodyHash(t[6:])]; len(ts) == 1 {
			l.exact = ts[0]
		}
		for {
			loc := trailingMedia.FindStringIndex(text)
			if loc == nil {
				break
			}
			text, l.media = text[:loc[0]], true
		}
		l.body = text
		lines = append(lines, l)
	}
	if len(a.days) >= alignDaysMost {
		clear(a.days)
	}
	a.days[key] = gatewayDay{mod: st.ModTime(), size: st.Size(), lines: lines}
	return lines
}

// gatewayChats are the gateway's chat ids by their normalized names, listed again after alignNamesEvery.
func (a *gatewayAligner) gatewayChats() map[string][]string {
	now := a.now()
	if a.names != nil && now.Sub(a.namesAt) < alignNamesEvery {
		return a.names
	}
	names := map[string][]string{}
	for _, c := range a.s.Chats() {
		if c.Source != gatewaySource {
			continue
		}
		if n := normalizeText(c.Name); n != "" {
			names[n] = append(names[n], c.ID)
		}
	}
	a.names, a.namesAt = names, now
	return names
}

func (a *gatewayAligner) walkPath() string {
	return filepath.Join(a.s.Dir, KindChat, crosswalkFile)
}

// crosswalk is the crosswalk as stored, read once.
func (a *gatewayAligner) crosswalk() map[string]string {
	if a.walk != nil {
		return a.walk
	}
	a.walk = map[string]string{}
	if ok, err := readJSON(a.walkPath(), &a.walk); err != nil || !ok {
		if err != nil {
			a.walkFail = true // a damaged file is left for the owner to see; L2 still finds duplicates
		}
		a.walk = map[string]string{}
	}
	return a.walk
}

// pair records that sync chat id is gateway chat gw. A failed write only costs the next message an L2 search.
func (a *gatewayAligner) pair(id, gw string) {
	if a.walkFail || a.s.ReadOnly() {
		return
	}
	a.walk[id] = gw
	raw, err := json.MarshalIndent(a.walk, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.walkPath()), 0o700); err != nil {
		return
	}
	_ = managerconf.WriteFileAtomic(a.walkPath(), raw, 0o600)
}

// messageFingerprint is a message's identity across sources: the hex SHA-256 of its chat's name, its sender, its
// 2-second bucket (Unix seconds / 2) and the hex SHA-256 of its body, joined by NULs, the texts normalized
// (normalizeText). It must stay byte-for-byte the Lark module's larksync.MessageFingerprint: the knowledge base cannot
// import the module, so both follow the one written rule (the test pins a vector).
func messageFingerprint(chatName, sender string, at time.Time, body string) string {
	b := sha256.Sum256([]byte(normalizeText(body)))
	sum := sha256.Sum256([]byte(normalizeText(chatName) + "\x00" + normalizeText(sender) + "\x00" +
		strconv.FormatInt(at.Unix()/2, 10) + "\x00" + hex.EncodeToString(b[:])))
	return hex.EncodeToString(sum[:])
}

// normalizeText is s trimmed, its runs of white space one space, in lower case.
func normalizeText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
