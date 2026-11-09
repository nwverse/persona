package hub

import (
	"bytes"
	"context"
	"hash/maphash"
	"net/http"
	"slices"
	"sync"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The inbox's lists (GET /api/v1/inbox and /inbox/done) are localized as they are merged, item by item, rather than
// in relayHTTP as one body: every app fetches the whole list again after each inbox push, and i18n.InboxJSON decoded
// and marshaled 400 items again on each GET (17-22 ms, 4-7 MB). InboxJSON does to a value what that value alone says,
// so an item localized on its own reads as it does within its answer, and the top level without the list does too;
// when anything in an answer changes, the whole answer is marshaled again, so every item and the top level are then
// written as InboxReencoded gives them. The bytes are InboxJSON's on the whole answer, then the merge's.
//
// What an item localizes to depends only on its bytes and the language, so it is kept by a 128-bit hash of its bytes,
// for each list route, manager and language, and only for the items of the latest answer: a GET after a push that
// changed one item localizes that one.

// inboxFormsMaxBody: a larger answer is localized whole, its items not kept.
const inboxFormsMaxBody = 16 << 20

// inboxSlotsMax and inboxFormsMaxKept bound what is kept: list routes x managers x languages, and the forms' bytes.
// Past either, every other slot is dropped.
const (
	inboxSlotsMax     = 64
	inboxFormsMaxKept = 64 << 20
)

// inboxSlot is whose items one generation keeps.
type inboxSlot struct {
	route, manager string
	lang           i18n.Lang
}

// inboxLocalize is a 2xx inbox list answer to be localized as it is merged.
type inboxLocalize struct {
	forms *inboxForms
	slot  inboxSlot
}

// inboxForms keeps the inbox list items' localized forms (Hub.inboxForms); the zero value is ready.
type inboxForms struct {
	mu    sync.Mutex
	seeds [2]maphash.Seed
	slots map[inboxSlot]inboxGen
	kept  int // the slots' bytes
}

// inboxGen is one slot's items as its latest answer had them.
type inboxGen struct {
	forms map[inboxKey]inboxForm // never changed once kept
	bytes int
}

// inboxKey is an item's bytes hashed with each of the two seeds.
type inboxKey [2]uint64

// inboxForm is what an item reads as when localized.
type inboxForm struct {
	changed bool      // InboxJSON changes it
	form    []byte    // changed: InboxJSON's; else InboxReencoded's once an answer needed it, nil before
	shape   itemShape // form's
}

// inboxListRaw marks a request on one of the inbox's lists asked by forwardInboxList: relayHTTP leaves a 2xx answer
// as the manager gave it and says so in kept.
type inboxListRaw struct{ kept bool }

type inboxListRawKey struct{}

func inboxListRawOf(r *http.Request) *inboxListRaw {
	raw, _ := r.Context().Value(inboxListRawKey{}).(*inboxListRaw)
	return raw
}

// forwardInboxList is forwardHTTP for the inbox's lists (mergedLists, pagedLists), asked into a bufferedWriter: a 2xx
// answer is kept as its manager gave it, for writeMerged to localize item by item; any other is localized as before.
func (h *Hub) forwardInboxList(w http.ResponseWriter, r *http.Request, id string) {
	bw, ok := w.(*bufferedWriter)
	l := h.requestLang(r)
	if !ok || !i18n.Valid(l) {
		h.forwardHTTP(w, r, id)
		return
	}
	raw := &inboxListRaw{}
	h.forwardHTTP(w, r.WithContext(context.WithValue(r.Context(), inboxListRawKey{}, raw)), id)
	if raw.kept {
		bw.localize = &inboxLocalize{forms: &h.inboxForms, slot: inboxSlot{route: r.URL.Path, manager: id, lang: l}}
	}
}

// scan cuts a as writeDecoded reads it, localized first when its relay left that to the merge.
func (a *listAnswer) scan(key string) (answerScan, scanUse) {
	var s answerScan
	var use scanUse
	if a.scanned != nil {
		s, use = a.scanned.s, a.scanned.use
	} else {
		s, use = scanAnswer(a.body, key)
	}
	if a.localize == nil || use != scanMerge {
		return s, use
	}
	if local, ok := a.localize.items(key, len(a.body), s); ok {
		return local, scanMerge
	}
	a.localizeWhole()
	return scanAnswer(a.body, key)
}

// localizeWhole localizes a kept inbox answer as relayHTTP localizes any other.
func (a *listAnswer) localizeWhole() {
	if a.localize == nil {
		return
	}
	if localized := i18n.InboxJSON(a.localize.slot.lang, a.body); !bytes.Equal(localized, a.body) {
		a.body = localized
		a.scanned = nil
		a.header.Del("Content-Length")
	}
	a.localize = nil
}

// items is s localized item by item, as InboxJSON on the whole answer would have made it; false when the answer is to
// be localized whole: its list is not "items", it is too big to keep, or its top level is an item (InboxJSON then
// visits that item's slots and not the list). A list that is null or missing reads as an empty one either way.
func (l *inboxLocalize) items(key string, size int, s answerScan) (answerScan, bool) {
	if key != "items" || size > inboxFormsMaxBody {
		return s, false
	}
	top := []byte{'{'}
	for i, m := range s.members {
		if i > 0 {
			top = append(top, ',')
		}
		top = append(top, '"')
		top = append(top, m.key...)
		top = append(top, '"', ':')
		top = append(top, m.val...)
	}
	top = append(top, '}')
	if i18n.InboxIsItem(top) {
		return s, false
	}
	topForm, items, changed := l.forms.localize(l.slot, top, s.items)
	if !changed {
		return s, true
	}
	members, _, plain := jsonMembers(topForm, jsonWS(topForm, 0), make([]jsonMember, 0, len(s.members)))
	if !plain {
		return s, false
	}
	slices.SortFunc(members, func(x, y jsonMember) int { return bytes.Compare(x.key, y.key) })
	return answerScan{members: members, items: items}, true
}

// localize is top (the answer's top level without its list) and items as InboxJSON on the whole answer writes them, and
// whether it changed anything; it keeps the items' forms for slot, only these.
func (f *inboxForms) localize(slot inboxSlot, top []byte, items []scannedItem) ([]byte, []scannedItem, bool) {
	l := slot.lang
	f.mu.Lock()
	if f.slots == nil {
		f.seeds = [2]maphash.Seed{maphash.MakeSeed(), maphash.MakeSeed()}
		f.slots = map[inboxSlot]inboxGen{}
	}
	seeds, kept := f.seeds, f.slots[slot].forms
	f.mu.Unlock()

	next := make(map[inboxKey]inboxForm, len(items))
	keys := make([]inboxKey, len(items))
	changed := false
	for i, it := range items {
		k := inboxKey{maphash.Bytes(seeds[0], it.raw), maphash.Bytes(seeds[1], it.raw)}
		form, ok := next[k]
		if !ok {
			if form, ok = kept[k]; !ok {
				form = inboxFormOf(l, it)
			}
			next[k] = form
		}
		keys[i] = k
		changed = changed || form.changed
	}
	topForm := i18n.InboxJSON(l, top)
	topChanged := !bytes.Equal(topForm, top)
	if changed = changed || topChanged; changed {
		if !topChanged {
			topForm = i18n.InboxReencoded(top)
		}
		out := make([]scannedItem, len(items))
		for i, it := range items {
			form := next[keys[i]]
			if form.form == nil {
				form.form = i18n.InboxReencoded(it.raw)
				_, form.shape = scanItem(form.form, 0)
				next[keys[i]] = form
			}
			out[i] = scannedItem{raw: form.form, shape: form.shape}
		}
		items = out
	}

	gen := inboxGen{forms: next}
	for _, form := range next {
		gen.bytes += len(form.form) + 64 // an entry's own size, roughly
	}
	f.keep(slot, gen)
	return topForm, items, changed
}

// keep makes gen slot's generation, in place of its last.
func (f *inboxForms) keep(slot inboxSlot, gen inboxGen) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.slots[slot]
	f.kept -= old.bytes
	if (!ok && len(f.slots) >= inboxSlotsMax) || f.kept+gen.bytes > inboxFormsMaxKept {
		clear(f.slots)
		f.kept = 0
	}
	f.slots[slot] = gen
	f.kept += gen.bytes
}

// inboxFormOf localizes one item alone.
func inboxFormOf(l i18n.Lang, it scannedItem) inboxForm {
	localized := i18n.InboxJSON(l, it.raw)
	if bytes.Equal(localized, it.raw) {
		return inboxForm{}
	}
	_, shape := scanItem(localized, 0)
	return inboxForm{changed: true, form: localized, shape: shape}
}
