package hub

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The merged lists against the code before #2807 (legacyWriteMerged, legacyInboxMerge), for 400 inbox items:
//
//	go test ./internal/hub/ -run '^$' -bench 'WriteMerged|MergedInbox' -benchmem

func BenchmarkWriteMerged(b *testing.B) {
	answers := []listAnswer{listOf("assistant", http.StatusOK, string(inboxBody(b, 400)))}
	for _, c := range []struct {
		name  string
		merge func(http.ResponseWriter, string, []listAnswer)
	}{{"decoded", legacyWriteMerged}, {"scanned", writeMerged}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c.merge(discardWriter{}, "items", answers)
			}
		})
	}
}

// A GET of the inbox: the old pipeline (the relay localizing the whole answer, then the merge), and the merge
// localizing item by item with nothing kept, with every item kept, and with one item changed since the last GET.
func BenchmarkMergedInbox(b *testing.B) {
	body := inboxBody(b, 400)
	other := bytes.Replace(body, []byte(`seed00007"`), []byte(`seed99999"`), 1)
	if bytes.Equal(other, body) {
		b.Fatal("no item to change")
	}
	header := http.Header{"Content-Type": {"application/json"}}
	merge := func(forms *inboxForms, l i18n.Lang, body []byte) {
		writeMerged(discardWriter{}, "items", []listAnswer{{id: "assistant", status: http.StatusOK, header: header, body: body,
			localize: &inboxLocalize{forms: forms, slot: inboxSlot{route: "/api/v1/inbox", manager: "assistant", lang: l}}}})
	}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans} {
		b.Run(string(l), func(b *testing.B) {
			b.Run("legacy", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					legacyWriteMerged(discardWriter{}, "items", []listAnswer{{id: "assistant", status: http.StatusOK, header: header,
						body: i18n.InboxJSON(l, body)}})
				}
			})
			b.Run("cold", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					merge(&inboxForms{}, l, body)
				}
			})
			b.Run("kept", func(b *testing.B) {
				forms := &inboxForms{}
				merge(forms, l, body)
				b.ReportAllocs()
				for b.Loop() {
					merge(forms, l, body)
				}
			})
			b.Run("one-changed", func(b *testing.B) {
				forms := &inboxForms{}
				merge(forms, l, body)
				bodies, n := [2][]byte{other, body}, 0
				b.ReportAllocs()
				for b.Loop() {
					merge(forms, l, bodies[n%2])
					n++
				}
			})
		})
	}
}
