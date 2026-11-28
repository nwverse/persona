package kblark

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/cliwire"
	"github.com/nwverse/persona/server/internal/kb"
)

func TestIsLarkLinkRequiresHTTPSAndCanonicalLarkHost(t *testing.T) {
	allowed := []string{
		"https://x.larkoffice.com/docx/d1",
		"https://example.feishu.cn/wiki/w1",
		"https://team.larksuite.com/sheets/s1",
		"https://feishu.cn/docx/d2",
		"https://larkoffice.com/minutes/m1",
		"https://larksuite.com/base/b1",
		"https://X.LARKOFFICE.COM./docx/d3",
	}
	for _, u := range allowed {
		if !isLarkLink(&kb.Item{URL: u}) {
			t.Errorf("isLarkLink rejected valid Lark URL %q", u)
		}
	}

	rejected := []string{
		"",
		"http://x.larkoffice.com/docx/d1",
		"https://evil.com/phish?x=feishu",
		"https://evil.com/lark/docx/d1",
		"https://feishu.evil.com/docx/d1",
		"https://evil-larkoffice.com/docx/d1",
		"https://larksuite.com.evil.com/docx/d1",
		"https://feishu.cn@evil.com/docx/d1",
		"https://user:pass@x.larkoffice.com/docx/d1",
		"javascript:feishu.cn/docx/d1",
		"file:///etc/feishu",
		"--config=feishu",
	}
	for _, u := range rejected {
		if isLarkLink(&kb.Item{URL: u}) {
			t.Errorf("isLarkLink accepted non-Lark/unsafe URL %q", u)
		}
	}
	if isLarkLink(nil) {
		t.Error("isLarkLink(nil) = true, want false")
	}
}

type recordingRunner struct {
	calls [][]string
	reply func(call int, args []string) ([]byte, error)
}

func (r *recordingRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	cp := append([]string(nil), args...)
	r.calls = append(r.calls, cp)
	return r.reply(len(r.calls), cp)
}

func (r *recordingRunner) InspectDocument(context.Context, string) (cliwire.Document, error) {
	return cliwire.Document{}, nil
}

func TestClientHardeningPaginationDateTimezoneAndFlagInjection(t *testing.T) {
	t.Run("SearchMinutes stops on repeated page_token instead of amplifying 20x", func(t *testing.T) {
		rr := &recordingRunner{
			reply: func(_ int, _ []string) ([]byte, error) {
				return []byte(`{"ok":true,"data":{"items":[{"token":"t1","display_info":"Weekly"}],"has_more":true,"page_token":"same-tok"}}`), nil
			},
		}
		c := NewClient(rr, time.UTC)
		minutes, err := c.SearchMinutes(context.Background(), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("SearchMinutes: %v", err)
		}
		if len(rr.calls) != 2 || len(minutes) != 1 {
			t.Fatalf("calls=%d minutes=%d, want 2 calls and 1 deduplicated minute", len(rr.calls), len(minutes))
		}
	})

	t.Run("SearchMinutes rejects leading-dash page_token and honors canceled ctx", func(t *testing.T) {
		rr := &recordingRunner{
			reply: func(_ int, _ []string) ([]byte, error) {
				return []byte(`{"ok":true,"data":{"items":[{"token":"t1","display_info":"Weekly"}],"has_more":true,"page_token":"--evil-flag"}}`), nil
			},
		}
		c := NewClient(rr, time.UTC)
		minutes, err := c.SearchMinutes(context.Background(), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("SearchMinutes: %v", err)
		}
		if len(rr.calls) != 1 || len(minutes) != 1 {
			t.Fatalf("expected SearchMinutes to stop before passing leading-dash page_token: calls=%v", rr.calls)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.SearchMinutes(ctx, time.Now().AddDate(0, 0, -1), time.Now()); err == nil {
			t.Fatal("expected error for canceled context")
		}
	})

	t.Run("MinuteDetail rejects empty or flag-like tokens", func(t *testing.T) {
		rr := &recordingRunner{
			reply: func(_ int, _ []string) ([]byte, error) {
				return []byte(`{"ok":true,"data":{"minutes":[{"title":"x"}]}}`), nil
			},
		}
		c := NewClient(rr, time.UTC)
		for _, bad := range []string{"", "-x", "--profile=evil", "tok with space", "tok\nline"} {
			if _, err := c.MinuteDetail(context.Background(), bad); err == nil {
				t.Errorf("MinuteDetail accepted unsafe token %q", bad)
			}
		}
		if len(rr.calls) != 0 {
			t.Fatalf("runner invoked for invalid tokens: %v", rr.calls)
		}
	})

	t.Run("SearchDocs passes --query=value safely and parses zoneless timestamps in client zone", func(t *testing.T) {
		tokyo := time.FixedZone("UTC+9", 9*3600)
		rr := &recordingRunner{
			reply: func(_ int, _ []string) ([]byte, error) {
				return []byte(`{"ok":true,"data":{"results":[{"result_meta":{"token":"d1","url":"https://x.larkoffice.com/docx/d1","update_time_iso":"2026-11-20 09:52:58"},"title_highlighted":"Plan"}]}}`), nil
			},
		}
		c := NewClient(rr, tokyo)
		docs, err := c.SearchDocs(context.Background(), "--flag-like-query")
		if err != nil {
			t.Fatalf("SearchDocs: %v", err)
		}
		if len(rr.calls) != 1 || !slices.Contains(rr.calls[0], "--query=--flag-like-query") {
			t.Fatalf("SearchDocs did not bind query with --query=...: %v", rr.calls)
		}
		want := time.Date(2026, 11, 20, 9, 52, 58, 0, tokyo)
		if len(docs) != 1 || !docs[0].At.Equal(want) || docs[0].At.Location() != tokyo {
			t.Fatalf("doc.At = %v (%v), want %v (%v)", docs[0].At, docs[0].At.Location(), want, tokyo)
		}
	})

	t.Run("parseStart rejects invalid calendar dates like Feb 31 instead of rolling over", func(t *testing.T) {
		c := NewClient(nil, time.UTC)
		if got := c.parseStart("Start: 2026-02-31 10:00:00"); !got.IsZero() {
			t.Fatalf("parseStart(2026-02-31) = %v, want zero time", got)
		}
		if got := c.parseStart("Start: 2026-04-31 10:00:00"); !got.IsZero() {
			t.Fatalf("parseStart(2026-04-31) = %v, want zero time", got)
		}
		valid := c.parseStart("Bad: 2026-02-31 10:00:00", "Real: 2026-02-28 10:00:00")
		want := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
		if !valid.Equal(want) {
			t.Fatalf("parseStart valid fallback = %v, want %v", valid, want)
		}
	})
}

func TestRunSkipsBlankDocTokensAndForeignLinks(t *testing.T) {
	e := newEngine(t)
	if err := e.Store.PutItem(&kb.Item{Kind: kb.KindDoc, Source: "other", ID: "evil", URL: "https://evil.com/?q=feishu"}); err != nil {
		t.Fatal(err)
	}
	r := &fakeReader{
		docs:     map[string]string{"https://evil.com/?q=feishu": "SHOULD NOT INSPECT"},
		searchOK: true,
		search: map[string][]Doc{
			"q": {
				{Token: "", Title: "blank-id", URL: "https://x.larkoffice.com/docx/blank"},
				{Token: "d1", Title: "good", URL: "https://x.larkoffice.com/docx/d1"},
			},
		},
	}
	res, err := Run(context.Background(), e, r, Config{DocSearch: []string{"q"}}, time.Now(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Filled != 0 {
		t.Fatalf("Filled = %d, want 0 (evil URL containing 'feishu' must not be inspected)", res.Filled)
	}
	if res.Docs != 1 {
		t.Fatalf("Docs = %d, want 1 (blank token must be skipped)", res.Docs)
	}
	for _, it := range e.Store.Items(kb.KindDoc) {
		if strings.TrimSpace(it.ID) == "" {
			t.Fatalf("blank doc ID was persisted: %+v", it)
		}
	}
}
