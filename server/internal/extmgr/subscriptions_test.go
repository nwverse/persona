package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// The subscriptions' routes: anyone reads, only full access changes, and the service's errors keep the API's body.
func TestSubscriptionRoutes(t *testing.T) {
	h := newTestManager(t)
	mux := http.NewServeMux()
	h.subscriptionRoutes(mux)
	call := func(p principal, method, path, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		mux.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), p)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	full, watch := principal{ID: "dev_phone", Role: roleFull}, principal{ID: "dev_watch", Role: roleAnswer}
	if code, out := call(full, "GET", "/api/v1/subscriptions", ""); code != http.StatusServiceUnavailable || out["code"] != "unavailable" {
		t.Fatalf("before the service runs: %d %v", code, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.startSubscriptions(ctx)
	if !h.subscriptionsShown() {
		t.Fatal("the apps are not told about subscriptions, which deliver now (#2039)")
	}
	if !subscriptions.New(subscriptions.Options{Features: subscriptions.Features{Deliver: true}}).Features().Deliver {
		t.Fatal("Features does not say what the service was made with")
	}
	code, out := call(watch, "GET", "/api/v1/subscriptions", "")
	if code != http.StatusOK || out["subscriptions"] == nil || out["features"] == nil {
		t.Fatalf("list from an answer-only device: %d %v", code, out)
	}
	if code, out := call(watch, "GET", "/api/v1/subscriptions/summary", ""); code != http.StatusOK || out["connectors"] == nil {
		t.Fatalf("summary: %d %v", code, out)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/subscriptions"}, {"PUT", "/api/v1/subscriptions/sub_x"}, {"DELETE", "/api/v1/subscriptions/sub_x"},
		{"POST", "/api/v1/subscriptions/sub_x/run"}, {"POST", "/api/v1/subscriptions/-/dry-run"},
	} {
		if code, out := call(watch, c.method, c.path, `{}`); code != http.StatusForbidden || out["code"] != "read_only_device" {
			t.Errorf("%s %s from an answer-only device: %d %v", c.method, c.path, code, out)
		}
	}
	if code, out := call(full, "POST", "/api/v1/subscriptions", `{"connector":"nowhere","source":"x"}`); code != http.StatusNotFound || out["code"] != "not_found" || out["error"] == nil {
		t.Fatalf("an unknown connector: %d %v", code, out)
	}
	if code, out := call(full, "GET", "/api/v1/subscriptions/sub_x", ""); code != http.StatusNotFound || out["code"] != "not_found" {
		t.Fatalf("an unknown subscription: %d %v", code, out)
	}
	if code, out := call(full, "POST", "/api/v1/subscriptions", `{"connector":`); code != http.StatusBadRequest || out["code"] != "bad_request" {
		t.Fatalf("broken JSON: %d %v", code, out)
	}
}

// A subscription is an internal source (#2039): every Inbox round names it and, unless it reads only the Feishu chats,
// hands in its things as a plugin's (triaged first); the knowledge base lists it and reads it through
// connectors.changes. Deleting it with its data asks both to remove theirs, and while the assistant is away the
// subscription stays, switched off, for another try.
func TestSubscriptionsAreInboxAndKnowledgeBaseSources(t *testing.T) {
	ctx := context.Background()
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	store := subscriptions.NewMemStore()
	now := time.Now().UTC()
	from, mark := now.Add(-time.Minute), now.Add(-30*time.Second)
	sub := subscriptions.Subscription{ID: "sub_example", Connector: "example", Source: "notices", Label: "Notices",
		Recipe: subscriptions.Recipe{Mode: "items", Kind: "sub_notice", GroupBy: "source"}, ToInbox: true, ToKB: true,
		InboxFrom: &from, InboxCatchup: &mark, KBFrom: &from, Revision: 1, CreatedAt: now.Add(-time.Hour)}
	if err := store.CreateSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-2 * time.Minute)
	items := []subscriptions.Item{{UID: "n1", Title: "Office hours moved", Time: &at, Body: "Made-up text", Kind: "sub_notice"},
		{UID: "n2", Title: "Form due", Time: &at, Body: "Made-up text", Kind: "sub_notice"}}
	if err := store.CommitRun(ctx, subscriptions.RunCommit{Sub: sub, Revision: 1, Items: items, Now: mark}); err != nil {
		t.Fatal(err)
	}
	h.subs = subscriptions.New(h.subscriptionOptions(store))

	r := h.readInbox(ctx, roundPoll)
	var src *assistantwire.InboxSource
	for i := range r.Sources {
		if r.Sources[i].ID == sub.ID {
			src = &r.Sources[i]
		}
	}
	if r.Names[sub.ID] != "Notices" || src == nil || !src.Extension || src.Kinds["sub_digest"] == "" || len(src.Things) != 3 {
		t.Fatalf("the round: names %v, source %+v", r.Names, src)
	}
	if chats := h.readInbox(ctx, roundChats); chats.Names[sub.ID] != "Notices" || len(chats.Sources) != 0 {
		t.Fatalf("a round of the Feishu chats: %+v", chats)
	}

	archives := h.kbArchives(ctx)
	if len(archives.Archives) != 1 || archives.Archives[0].ID != sub.ID {
		t.Fatalf("the knowledge base's archives: %+v", archives)
	}
	raw, _, err := h.kbSourcesCall(ctx, assistantwire.AppCall{AppID: assistantwire.AppConnectors, Method: assistantwire.ConnectorsChanges,
		Params: json.RawMessage(`{"id":"sub_example","cursor":""}`)})
	var res assistantwire.ChangesResult
	_ = json.Unmarshal(raw, &res)
	if err != nil || len(res.Batches) != 1 || len(res.Batches[0].Messages) != 2 || res.Next == "" {
		t.Fatalf("connectors.changes of the subscription: %+v %v", res, err)
	}
	if _, _, err := h.kbSourcesCall(ctx, assistantwire.AppCall{AppID: assistantwire.AppConnectors, Method: assistantwire.ConnectorsArchiveMedia,
		Params: json.RawMessage(`{"id":"sub_example","ref":"x"}`)}); err == nil {
		t.Fatal("a subscription handed over an attachment")
	}

	mux := http.NewServeMux()
	h.subscriptionRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/subscriptions/sub_example?data=1", nil)
	mux.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), principal{ID: "dev_phone", Role: roleFull})))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusServiceUnavailable || out["code"] != assistantwire.CodeAssistantOffline {
		t.Fatalf("deleting its data with the assistant away: %d %v", rec.Code, out)
	}
	if left, _ := store.GetSubscription(ctx, sub.ID); left == nil || left.ToInbox || left.ToKB {
		t.Fatalf("after the failed removal: %+v", left)
	}
	if a := h.kbArchives(ctx); len(a.Archives) != 0 {
		t.Fatalf("the knowledge base still reads a subscription switched off: %+v", a)
	}
}
