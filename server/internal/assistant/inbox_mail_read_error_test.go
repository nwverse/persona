package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// collectedJSON hands the inbox a round as the wire carries it (inbox.collected through the bus's decoding).
func collectedJSON(t *testing.T, h *inboxHub, round string) {
	t.Helper()
	decode := func(v any) error { return json.Unmarshal([]byte(round), v) }
	if _, ok, err := inboxRequest(context.Background(), h.Service, assistantwire.MethodInboxCollected, decode); !ok || err != nil {
		t.Fatalf("inbox.collected: %v %v", ok, err)
	}
}

// mailAccountJSON is one mail account with one unread mail, as a round lists it.
func mailAccountJSON(id, email string, uid int) string {
	return fmt.Sprintf(`{"id":%q,"email":%q,"messages":[{"mailbox":"INBOX","uid":%d,"message_id":"<%d@example.com>",`+
		`"subject":"Report %d","from":{"name":"Bob","email":"bob@example.com"},"to":[{"name":"","email":%q}],`+
		`"date":%q,"snippet":"hello","seen":false}]}`, id, email, uid, uid, uid, email, time.Now().UTC().Format(time.RFC3339))
}

// A mail account the round could not read shows in the inbox status like a connector that could not be read
// (#1849): GET /api/v1/inbox names it in triage.sources (source "mail:<account>", its address as the name) with the
// read code and the words for it, and its mail stays listed. A round without mail (a poll) leaves that as it was; the
// next mail round that reads the account clears it.
func TestInboxSaysWhichMailAccountCouldNotBeRead(t *testing.T) {
	h := newInboxHub(t)
	names := `"names":{"mail":"Mail"}`
	collectedJSON(t, h, `{"round":"full","sources":[],`+names+`,"mail":{"accounts":[`+
		mailAccountJSON("acc-me", "me@example.com", 1)+`,`+mailAccountJSON("acc-work", "work@example.com", 1)+`]}}`)
	if items, tr := inboxTriageOf(t, h); len(items) != 2 || len(tr.Sources) != 0 {
		t.Fatalf("after a round that read both accounts: %d items, triage %+v", len(items), tr)
	}

	collectedJSON(t, h, `{"round":"full","sources":[],`+names+`,"mail":{"accounts":[`+mailAccountJSON("acc-me", "me@example.com", 1)+
		`],"failed":[{"id":"acc-work","email":"work@example.com","read_error":"timeout"}]}}`)
	items, tr := inboxTriageOf(t, h)
	if len(items) != 2 {
		t.Fatalf("the account that could not be read lost its mail: %d items", len(items))
	}
	want := InboxTriageSource{Source: "mail:acc-work", Name: "work@example.com", State: "ok", ReadError: assistantwire.ReadTimeout,
		ReadReason: i18n.T(h.lang(context.Background()), "assistant-inbox_admit.read_timeout")}
	if len(tr.Sources) != 1 || tr.Sources[0] != want || tr.State != "ok" || tr.Waiting != 0 {
		t.Fatalf("triage after a mail account could not be read: %+v, want the source %+v", tr, want)
	}

	collectedJSON(t, h, `{"round":"poll","sources":[],`+names+`}`)
	if _, tr := inboxTriageOf(t, h); len(tr.Sources) != 1 || tr.Sources[0].Source != "mail:acc-work" {
		t.Fatalf("a round without mail cleared the mail account's failure: %+v", tr)
	}

	collectedJSON(t, h, `{"round":"full","sources":[],`+names+`,"mail":{"accounts":[`+
		mailAccountJSON("acc-me", "me@example.com", 1)+`,`+mailAccountJSON("acc-work", "work@example.com", 1)+`]}}`)
	if _, tr := inboxTriageOf(t, h); len(tr.Sources) != 0 {
		t.Fatalf("a mail account read again still says it failed: %+v", tr)
	}
}
