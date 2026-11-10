package hub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const ringReply = "今天吃了这些：\n\n```persona-card\n" +
	`{"type":"ring","title":"今天的热量","total":{"value":2000,"unit":"kcal"},"segments":[{"label":"早餐","value":480},{"label":"午餐","value":720}],"note":"还能吃 800 kcal"}` +
	"\n```\n\n继续加油。"

// cardsIn is every persona-card of md, parsed.
func cardsIn(t *testing.T, md string) []wire.PersonaCard {
	t.Helper()
	var out []wire.PersonaCard
	for _, b := range wire.AssistantViewBlocks(md) {
		if b.Language != "persona-card" {
			continue
		}
		var c wire.PersonaCard
		if err := json.Unmarshal(b.Raw, &c); err != nil {
			t.Fatalf("a card that is not JSON: %v\n%s", err, b.Raw)
		}
		out = append(out, c)
	}
	return out
}

// A ring an older app cannot draw becomes the stacked_bar it has always drawn: the same parts, what the total leaves
// a "remaining" rest, the text around it as it was.
func TestRingBecomesAStackedBarForAClientWithoutRings(t *testing.T) {
	got := ringCardsDowngraded(i18n.ZhHans, ringReply)
	if !strings.HasPrefix(got, "今天吃了这些：\n\n```persona-card\n") || !strings.HasSuffix(got, "\n```\n\n继续加油。") {
		t.Fatalf("the text around the card changed:\n%s", got)
	}
	cards := cardsIn(t, got)
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1:\n%s", len(cards), got)
	}
	c := cards[0]
	if c.Type != wire.PersonaCardKindStackedBar || c.Title != "今天的热量" || c.Note != "还能吃 800 kcal" || c.Total == nil || c.Total.Value != 2000 ||
		len(c.Segments) != 2 || c.Rest == nil || c.Rest.Label != "剩余" || c.Rest.Value != 800 {
		t.Fatalf("downgraded card = %+v", c)
	}
	if c.View() == nil {
		t.Fatal("the stacked_bar it became is one the apps cannot draw")
	}
	if en := cardsIn(t, ringCardsDowngraded(i18n.En, ringReply))[0]; en.Rest == nil || en.Rest.Label != "Remaining" {
		t.Fatalf("English rest = %+v", en.Rest)
	}
}

// A ring the apps could not draw either (one part, a negative part) is written as words, never left as JSON.
func TestUndrawableRingBecomesWords(t *testing.T) {
	md := "```persona-card\n" + `{"type":"ring","title":"热量","unit":"kcal","segments":[{"label":"早餐","value":480,"note":"燕麦"}],"note":"偏少"}` + "\n```"
	got := ringCardsDowngraded(i18n.ZhHans, md)
	want := "**热量**\n- 早餐: 480 kcal (燕麦)\n\n偏少"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	neg := "```persona-card\n" + `{"type":"ring","title":"差额","total":{"value":100,"unit":"g"},"segments":[{"label":"a","value":-5},{"label":"b","value":20}]}` + "\n```\n"
	got = ringCardsDowngraded(i18n.ZhHans, neg)
	if strings.Contains(got, "{") || strings.Contains(got, "persona-card") || !strings.Contains(got, "- 合计: 100 g") || !strings.Contains(got, "- 剩余: 85 g") {
		t.Fatalf("negative ring = %q", got)
	}
}

// Only rings change: other cards, a ring shown as an example inside another code block, and a ring still being
// written stay exactly as they are.
func TestOnlyRingsAreRewritten(t *testing.T) {
	for _, md := range []string{
		"```persona-card\n" + `{"type":"stacked_bar","segments":[{"label":"a","value":1}]}` + "\n```",
		"````markdown\n```persona-card\n" + `{"type":"ring","segments":[{"label":"a","value":1},{"label":"b","value":2}]}` + "\n```\n````",
		"```persona-card\n" + `{"type":"ring","segments":[{"label":"a","value":1}`,
		"ring 一词，没有卡片",
	} {
		if got := ringCardsDowngraded(i18n.ZhHans, md); got != md {
			t.Fatalf("changed:\n%s\n→\n%s", md, got)
		}
	}
}

func TestCardCompatItemsTouchesOnlyAssistantTextAndKeepsTheNodesSlice(t *testing.T) {
	items := []proto.Item{
		{ID: "u", Role: "user", Kind: proto.KindText, Text: ringReply},
		{ID: "a", Role: "assistant", Kind: proto.KindText, Text: ringReply},
		{ID: "t", Role: "assistant", Kind: proto.KindToolResult, Text: ringReply},
	}
	if got := cardCompatItems(i18n.ZhHans, true, items); &got[0] != &items[0] {
		t.Fatal("a client that draws rings got a copy")
	}
	got := cardCompatItems(i18n.ZhHans, false, items)
	if got[0].Text != ringReply || got[2].Text != ringReply {
		t.Fatal("a user message or tool result was rewritten")
	}
	if strings.Contains(got[1].Text, `"ring"`) {
		t.Fatalf("the assistant's ring went out: %s", got[1].Text)
	}
	if items[1].Text != ringReply {
		t.Fatal("the node's slice was written to")
	}
}

// What each app gets pushed for the same reply: the iOS builds out before the ring (no feature) and an Android without
// it get a stacked_bar; a new iOS build, Android and the web, which declare persona_card_ring, get the ring.
func TestEachClientGetsTheRingOrAStackedBarPushed(t *testing.T) {
	type app struct {
		name     string
		platform string
		features []string
		wantRing bool
	}
	apps := []app{
		{"old iOS", "ios-app", []string{"elicitation"}, false},
		{"new iOS", "ios-app", []string{"elicitation", featureCardRing}, true},
		{"Android without the feature", "android", []string{"elicitation"}, false},
		{"Android", "android", []string{"elicitation", featureCardRing}, true},
		{"web", "web", []string{"elicitation", featureCardRing}, true},
	}
	h := &Hub{clients: map[*client]struct{}{}}
	clients := map[string]*client{}
	for _, a := range apps {
		c := &client{out: make(chan []byte, 4), subs: map[[2]string]bool{{"m1", "s1"}: true}}
		c.meta.Platform = a.platform
		c.meta.features = map[string]bool{}
		for _, f := range a.features {
			c.meta.features[f] = true
		}
		addTestClient(h, c)
		clients[a.name] = c
	}
	h.itemsToSubscribers("m1", "s1", []proto.Item{{ID: "a1", Seq: 1, Role: "assistant", Kind: proto.KindText, Text: ringReply}}, itemSpan{}, false)
	for _, a := range apps {
		var e struct {
			Items []proto.Item `json:"items"`
		}
		if err := json.Unmarshal(<-clients[a.name].out, &e); err != nil || len(e.Items) != 1 {
			t.Fatalf("%s: %v %+v", a.name, err, e)
		}
		c := cardsIn(t, e.Items[0].Text)
		if len(c) != 1 {
			t.Fatalf("%s: cards %d", a.name, len(c))
		}
		if a.wantRing && c[0].Type != wire.PersonaCardKindRing {
			t.Fatalf("%s got %s, want the ring", a.name, c[0].Type)
		}
		if !a.wantRing && (c[0].Type != wire.PersonaCardKindStackedBar || c[0].View() == nil) {
			t.Fatalf("%s got %s, want a drawable stacked_bar", a.name, c[0].Type)
		}
	}
}

// A REST page declares rings in X-Persona-Features, or by matching a connection of its device that declared them.
func TestRequestWantsRingsByHeaderOrMatchingConnection(t *testing.T) {
	c := &client{device: "d1"}
	c.meta.Platform, c.meta.Version = "ios-app", "2.0+300"
	c.meta.features = map[string]bool{featureCardRing: true}
	h := &Hub{clients: map[*client]struct{}{c: {}}}
	req := func(client string, header string) bool {
		r := httptest.NewRequest("GET", "/api/v1/sessions/m1/s1/items", nil)
		ctx := context.WithValue(r.Context(), principalKey{}, principal{ID: "d1", Role: roleFull})
		ctx = context.WithValue(ctx, requestClientKey{}, parseRequestClient(client))
		if header != "" {
			r.Header.Set(featuresHeader, header)
		}
		return h.requestWants(r.WithContext(ctx), featureCardRing)
	}
	if !req("ios-app/2.0+300", "") {
		t.Fatal("the new iOS build's page did not match its connection")
	}
	if req("ios-app/1.9+250", "") {
		t.Fatal("an older build of the same device was taken for one that draws rings")
	}
	if req("", "") {
		t.Fatal("a request naming no client was taken for one that draws rings")
	}
	if !req("android/1", "elicitation, persona_card_ring") {
		t.Fatal("the X-Persona-Features header was not read")
	}
}
