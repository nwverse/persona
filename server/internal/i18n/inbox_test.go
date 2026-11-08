package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEnglishOriginInboxErrors(t *testing.T) {
	err := ErrEnglish("inbox-errors.no_item")
	if err.Error() != "no such inbox item" || Localize(err, "") != err.Error() {
		t.Fatal("legacy English error changed")
	}
	for _, l := range []Lang{En, ZhHans, Ja} {
		want := T(l, "inbox-errors.no_item")
		if got := Localize(err, l); got != want {
			t.Errorf("%s typed error: %q, want %q", l, got, want)
		}
		if got := Localize(errors.New(err.Error()), l); got != want {
			t.Errorf("%s RPC-flattened error: %q, want %q", l, got, want)
		}
	}
}

func TestInboxTextCatalogRoundTrip(t *testing.T) {
	for _, from := range []Lang{En, ZhHans, Ja} {
		for key := range catalogs()[from] {
			if !strings.HasPrefix(key, "inbox-errors.") {
				continue
			}
			args := Args{"actual": 2, "expected": 3, "target": `"xx"`, "max": 40, "repo": `"example"`}
			for _, to := range []Lang{En, ZhHans, Ja} {
				if got, want := InboxText(to, T(from, key, args)), T(to, key, args); got != want {
					t.Errorf("%s -> %s %s: %q, want %q", from, to, key, got, want)
				}
			}
		}
	}
}

func TestInboxJSONKeepsStoredDataAndUserContent(t *testing.T) {
	raw := []byte(`{"items":[{"id":"example","source":"mail","kind":"mail","title":"no such inbox item","preview":"no such inbox item","reason":"no such inbox item","triaged_by":"model","context":"no such inbox item","metadata":{"error":"no such inbox item"},"agent":{"error":"no such inbox item"},"handling":{"error":"the inbox item could not be saved"},"chain":[{"kind":"failed","text":"处理失败","detail":"no such inbox item"}],"mail":{"uid":9007199254740993}}],"triage":{"reason":"no such inbox item"}}`)
	for _, l := range []Lang{ZhHans, Ja} {
		out := InboxJSON(l, raw)
		var v struct {
			Items []struct {
				Title, Preview, Reason, Context string
				Metadata                        map[string]string
				Agent                           map[string]string
				Handling                        map[string]string
			}
		}
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatal(err)
		}
		it := v.Items[0]
		if it.Agent["error"] != T(l, "inbox-errors.no_item") || it.Handling["error"] != T(l, "inbox-errors.save_failed") {
			t.Fatalf("%s: response errors not localized: %s", l, out)
		}
		for _, s := range []string{it.Title, it.Preview, it.Reason, it.Context, it.Metadata["error"]} {
			if s != "no such inbox item" {
				t.Fatal("user/model/opaque text changed")
			}
		}
		if !strings.Contains(string(out), "9007199254740993") {
			t.Fatal("large opaque integer lost precision")
		}
	}
	if string(raw) != string(InboxJSON("", raw)) {
		t.Fatal("legacy response changed")
	}
	if !strings.Contains(string(raw), `"agent":{"error":"no such inbox item"}`) {
		t.Fatal("stored data mutated")
	}
	for _, opaque := range []string{`{"ok":true}`, `{"error":"third-party diagnostic"}`, `{"texts":["no such inbox item"]}`, `not JSON`, `{"error":"no such inbox item"} extra`} {
		if string(InboxJSON(Ja, []byte(opaque))) != opaque {
			t.Fatalf("opaque response changed: %s", opaque)
		}
	}
}

type inboxFieldError struct{}

func (inboxFieldError) Error() string      { return "field: no such inbox item" }
func (inboxFieldError) Text(l Lang) string { return "field: " + T(l, "inbox-errors.no_item") }

func TestTypedErrorAndNestedErrorArgs(t *testing.T) {
	field := inboxFieldError{}
	if Localize(field, Ja) != "field: "+T(Ja, "inbox-errors.no_item") {
		t.Fatal("typed field error not localized")
	}
	wrapped := fmt.Errorf("outer: %w", field)
	if Localize(wrapped, Ja) != wrapped.Error() {
		t.Fatal("wrapper context was discarded")
	}
	err := ErrEnglish("inbox-errors.translation_target", Args{"target": ErrEnglish("inbox-errors.no_item")})
	if Localize(err, Ja) != T(Ja, "inbox-errors.translation_target", Args{"target": T(Ja, "inbox-errors.no_item")}) {
		t.Fatal("nested error not localized")
	}
}
