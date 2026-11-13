package kb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// gramRunes is what the random texts of these tests are made of: both cases, digits, punctuation, every kind of
// white space a query is split at, CJK, kana, Hangul, full-width forms, an emoji, a combining accent (a text with it
// folds to another one) and letters whose lower case is another length.
var gramRunes = []rune("abcdeXYZ019.,-_/#+: \n\t 　的一是周报进度同步ひらカタ한글ＡＢａ１🙂é́ÉßİΣς")

func randomText(rng *rand.Rand, n int) string {
	var sb strings.Builder
	for range n {
		if rng.IntN(5) == 0 {
			sb.WriteByte(' ')
		}
		sb.WriteRune(gramRunes[rng.IntN(len(gramRunes))])
	}
	return sb.String()
}

// randomQuery is a query for text: mostly a run of the text as written or as folded (white space in it makes it
// several words), sometimes runes of its own (rarely in the text).
func randomQuery(rng *rand.Rand, text string) string {
	from := []rune(text)
	switch rng.IntN(4) {
	case 0:
		return randomText(rng, 1+rng.IntN(4))
	case 1:
		from = []rune(fold(text))
	}
	if len(from) == 0 {
		return "a"
	}
	start := rng.IntN(len(from))
	return string(from[start:min(len(from), start+1+rng.IntN(6))])
}

// A file that holds every word of a query is never one the keys rule out: what the index may skip is decided by
// this alone.
func TestGramsNeverRuleOutAFileThatHoldsTheWords(t *testing.T) {
	rng := rand.New(rand.NewPCG(1273, 1))
	held, ruledOut := 0, 0
	for range 4000 {
		text := randomText(rng, 1+rng.IntN(120))
		folded := fold(text)
		keys := fileGrams(folded)
		for range 25 {
			q := randomQuery(rng, text)
			ws := words(q)
			if len(ws) == 0 {
				continue
			}
			clauses, ok := queryClauses(ws)
			if !ok {
				t.Fatalf("no clauses for %q", q)
			}
			may := keysMay(keys, clauses)
			switch has := hasAll(folded, ws); {
			case has && !may:
				t.Fatalf("the keys of %q rule out %q, which it holds", text, q)
			case has:
				held++
			case !may:
				ruledOut++
			}
		}
	}
	// Both happen, or the test says nothing: files that hold the words, and files the keys rule out.
	if held < 1000 || ruledOut < 1000 {
		t.Fatalf("%d queries held, %d ruled out: the texts and queries no longer cover both", held, ruledOut)
	}
}

func FuzzGramsNeverRuleOutAFileThatHoldsTheWords(f *testing.F) {
	f.Add("今天的进度同步一下 build ok", "进度 BUILD")
	f.Add("café ＡＢ", "café")
	f.Add("a b", "a")
	f.Fuzz(func(t *testing.T, text, query string) {
		folded := fold(text)
		ws := words(query)
		clauses, ok := queryClauses(ws)
		if !ok {
			return
		}
		if hasAll(folded, ws) && !keysMay(fileGrams(folded), clauses) {
			t.Fatalf("the keys of %q rule out %q, which it holds", text, query)
		}
	})
}

// What the keys say of the queries the design has to answer one by one: a single CJK character, two letters, a
// word that ends or starts in the other script, punctuation, case, composed and decomposed text.
func TestGramsOfShortAndMixedQueries(t *testing.T) {
	text := "- 09:07 同事1: Quarterly 周报 draft is ready, C++ v1.2 (café) ＡＢ #42\n- 09:14 同事2: 进度ok 🙂"
	keys := fileGrams(fold(text))
	for _, c := range []struct {
		query string
		may   bool
	}{
		{"周", true}, {"报", true}, {"周报", true}, {"报周", false}, {"月", false},
		{"q", true}, {"qu", true}, {"QUARTERLY", true}, {"uart", true}, {"quarterlies", false},
		{"dr", true}, {"rd", false}, {"x", false},
		{"报 dr", true}, {"周报 draft", true}, {"周报 drift", false},
		{"事1", true}, {"1:", true}, {"度ok", true}, {"度o", true}, {"ok", true}, {"度k", false},
		{"c++", true}, {"++", true}, {"+", true}, {"c+-", false}, {"v1.2", true}, {"1.3", false}, {"(", true},
		{"café", true}, {"CAFÉ", true}, {"cafe", false},
		{"ＡＢ", true}, {"ａｂ", true}, {"ab", false},
		{"🙂", true}, {"🙃", false}, {"#42", true}, {"#4", true}, {"42", true}, {"#43", false},
	} {
		ws := words(c.query)
		clauses, ok := queryClauses(ws)
		if !ok {
			t.Fatalf("%q: no clauses", c.query)
		}
		if got := keysMay(keys, clauses); got != c.may {
			t.Errorf("%q: may = %v, want %v", c.query, got, c.may)
		}
		if has := hasAll(fold(text), ws); has && !c.may {
			t.Errorf("%q: the table is wrong, the text holds it", c.query)
		}
	}
}

// testDocs are n files with random texts, as an update reads them.
func testDocs(rng *rand.Rand, prefix string, n int) ([]builtDoc, map[string]string) {
	docs := make([]builtDoc, n)
	texts := map[string]string{}
	for i := range docs {
		name := fmt.Sprintf("%s/c%02d/messages/2026-%02d-%02d.md", prefix, i%7, 1+i%12, 1+i%28)
		if _, twice := texts[name]; twice {
			name += fmt.Sprintf(".%d", i)
		}
		text := "build " + randomText(rng, 5+rng.IntN(60))
		if i%3 == 0 {
			text += " weekly"
		}
		texts[name] = text
		docs[i] = builtDoc{segDoc: segDoc{name: name, mod: int64(1_700_000_000_000_000_000 + i), size: int64(len(text))}, keys: fileGrams(fold(text))}
	}
	return docs, texts
}

func testQueries(rng *rand.Rand, texts map[string]string) []string {
	out := []string{"build", "weekly", "build weekly", "的", "a", "ab", "周报", "zzzz", "#", "é", "ｂ"}
	names := make([]string, 0, len(texts))
	for name := range texts {
		names = append(names, name)
	}
	sort.Strings(names)
	for range 60 {
		out = append(out, randomQuery(rng, texts[names[rng.IntN(len(names))]]))
	}
	return out
}

// checkSegment: for every query, the segment names exactly the files whose own keys do not rule the query out.
func checkSegment(t *testing.T, seg *indexSeg, texts map[string]string, queries []string) {
	t.Helper()
	for _, q := range queries {
		clauses, ok := queryClauses(words(q))
		if !ok {
			continue
		}
		cand, err := seg.candidates(clauses)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		for id, d := range seg.docs {
			text, ok := texts[d.name]
			if !ok {
				t.Fatalf("the segment has %s, which is not one of its files", d.name)
			}
			if want := keysMay(fileGrams(fold(text)), clauses); cand.has(uint32(id)) != want {
				t.Fatalf("%q: %s is a candidate = %v, its keys say %v", q, d.name, !want, want)
			}
		}
	}
}

func TestSegmentAnswersAsItsFilesKeysDo(t *testing.T) {
	rng := rand.New(rand.NewPCG(1273, 2))
	for _, n := range []int{1, 2, 7, 8, 9, 63, 64, 65, 300} {
		docs, texts := testDocs(rng, "chats", n)
		path := filepath.Join(t.TempDir(), "seg-000001.idx")
		if err := writeSeg(path, docs); err != nil {
			t.Fatal(err)
		}
		seg, err := openSeg(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(seg.docs) != n || !sort.SliceIsSorted(seg.docs, func(i, j int) bool { return seg.docs[i].name < seg.docs[j].name }) {
			t.Fatalf("%d files: the segment lists %d, sorted by name: %v", n, len(seg.docs), seg.docs)
		}
		for _, d := range docs {
			id, ok := slices.BinarySearchFunc(seg.docs, d.name, func(s segDoc, name string) int { return strings.Compare(s.name, name) })
			if !ok || seg.docs[id] != d.segDoc {
				t.Fatalf("%s: read back as %+v, written as %+v", d.name, seg.docs[id], d.segDoc)
			}
		}
		checkSegment(t, seg, texts, testQueries(rng, texts))
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("the segment's mode is %v (%v), want 0600", fi.Mode().Perm(), err)
		}
	}
}

// Every kind of list is written and read: all the files, a bitmap, and gaps.
func TestSegmentListKinds(t *testing.T) {
	const n = 100
	docs := make([]builtDoc, n)
	for i := range docs {
		keys := []uint64{10} // every file
		if i%2 == 0 {
			keys = append(keys, 20) // half of them: a bitmap
		}
		if i == 3 || i == 97 {
			keys = append(keys, 30) // two: gaps
		}
		docs[i] = builtDoc{segDoc: segDoc{name: fmt.Sprintf("docs/d%03d.json", i), mod: int64(i), size: 1}, keys: keys}
	}
	path := filepath.Join(t.TempDir(), "seg-000001.idx")
	if err := writeSeg(path, docs); err != nil {
		t.Fatal(err)
	}
	seg, err := openSeg(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := allSegKeys(t, seg)
	if len(keys) != 3 {
		t.Fatalf("the segment has %d keys, want 3", len(keys))
	}
	for i, want := range []struct {
		kind  byte
		count int
	}{{listEvery, n}, {listBitmap, n / 2}, {listGaps, 2}} {
		k := keys[i]
		if got := raw[seg.lists+int64(k.start)]; got != want.kind {
			t.Errorf("key %d: list kind %d, want %d", k.key, got, want.kind)
		}
		cand, err := seg.candidates([]gramClause{{all: []uint64{k.key}}})
		if err != nil || cand.count() != want.count {
			t.Errorf("key %d: %d files (%v), want %d", k.key, cand.count(), err, want.count)
		}
		ids, err := seg.ids(nil, raw[seg.lists+int64(k.start):seg.lists+int64(k.end)], k.crc)
		if err != nil || len(ids) != want.count || !slices.IsSorted(ids) {
			t.Errorf("key %d: ids %v (%v), want %d ascending", k.key, ids, err, want.count)
		}
	}
	// A range of keys is the files of any of them; a key the segment does not have is no file.
	for _, c := range []struct {
		clause gramClause
		count  int
	}{
		{gramClause{lo: 15, hi: 31}, n/2 + 2}, {gramClause{lo: 21, hi: 31}, 2}, {gramClause{lo: 31, hi: 99}, 0},
		{gramClause{all: []uint64{20, 30}}, 0}, {gramClause{all: []uint64{10, 30}}, 2}, {gramClause{all: []uint64{25}}, 0},
	} {
		cand, err := seg.candidates([]gramClause{c.clause})
		if err != nil || cand.count() != c.count {
			t.Errorf("%+v: %d files (%v), want %d", c.clause, cand.count(), err, c.count)
		}
	}
}

// A merge keeps the files it is told to keep, with the keys they had.
func TestMergeSegmentsKeepsTheChosenFiles(t *testing.T) {
	rng := rand.New(rand.NewPCG(1273, 3))
	dir := t.TempDir()
	texts := map[string]string{}
	var inputs []mergeInput
	kept := 0
	for i, n := range []int{40, 1, 130} {
		docs, own := testDocs(rng, fmt.Sprintf("chats/s%d", i), n)
		path := filepath.Join(dir, fmt.Sprintf("seg-%06d.idx", i))
		if err := writeSeg(path, docs); err != nil {
			t.Fatal(err)
		}
		seg, err := openSeg(path)
		if err != nil {
			t.Fatal(err)
		}
		in := mergeInput{seg: seg}
		if i == 2 {
			in.keep = func(id uint32) bool { return id%3 != 0 }
		}
		for id, d := range seg.docs {
			if in.keep == nil || in.keep(uint32(id)) {
				texts[d.name] = own[d.name]
				kept++
			}
		}
		inputs = append(inputs, in)
	}
	path := filepath.Join(dir, "seg-000009.idx")
	n, err := mergeSegs(path, inputs)
	if err != nil || n != kept {
		t.Fatalf("merged %d files (%v), want %d", n, err, kept)
	}
	seg, err := openSeg(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(seg.docs) != kept {
		t.Fatalf("the merged segment lists %d files, want %d", len(seg.docs), kept)
	}
	checkSegment(t, seg, texts, testQueries(rng, texts))
	// The same file kept from two segments is refused: one of the two entries would be the wrong one.
	twice := filepath.Join(dir, "seg-000010.idx")
	if _, err := mergeSegs(twice, []mergeInput{{seg: inputs[0].seg}, {seg: inputs[0].seg}}); err == nil {
		t.Fatal("a file kept from two segments was merged")
	}
	if _, err := os.Stat(twice); !os.IsNotExist(err) {
		t.Fatalf("the refused merge left its file: %v", err)
	}
}

// A damaged segment is never believed: whatever byte is wrong or missing, it does not open, or the list a search
// reads does not check out, or the answer is still the right one (the byte was in a list the search did not read).
func TestDamagedSegmentIsNeverBelieved(t *testing.T) {
	rng := rand.New(rand.NewPCG(1273, 4))
	docs, texts := testDocs(rng, "chats", 24)
	dir := t.TempDir()
	good := filepath.Join(dir, "seg-000001.idx")
	if err := writeSeg(good, docs); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	queries := testQueries(rng, texts)[:30]
	bad := filepath.Join(dir, "seg-000002.idx")
	refused, caught := 0, 0
	check := func(what string, damaged []byte) {
		t.Helper()
		if err := os.WriteFile(bad, damaged, 0o600); err != nil {
			t.Fatal(err)
		}
		seg, err := openSeg(bad)
		if err != nil {
			refused++
			return
		}
		for _, q := range queries {
			clauses, ok := queryClauses(words(q))
			if !ok {
				continue
			}
			cand, err := seg.candidates(clauses)
			if err != nil {
				if !damaged2(err) {
					t.Fatalf("%s: %q failed with %v, which does not say the segment is damaged", what, q, err)
				}
				caught++
				continue
			}
			for id, d := range seg.docs {
				text, ok := texts[d.name]
				if !ok {
					t.Fatalf("%s: opened with a file it never had, %q", what, d.name)
				}
				if hasAll(fold(text), words(q)) && !cand.has(uint32(id)) {
					t.Fatalf("%s: %q rules out %s, which holds it", what, q, d.name)
				}
			}
		}
	}
	for i := range raw {
		damaged := bytes.Clone(raw)
		damaged[i] ^= 1 << (i % 8)
		check(fmt.Sprintf("byte %d flipped", i), damaged)
	}
	for n := 0; n < len(raw); n += 1 + n/16 {
		check(fmt.Sprintf("cut to %d bytes", n), raw[:n])
	}
	check("a byte added", append(bytes.Clone(raw), 0))
	if refused == 0 || caught == 0 {
		t.Fatalf("%d damaged segments refused, %d lists caught: the test no longer reaches both", refused, caught)
	}
}

func damaged2(err error) bool { return damaged(err) }

// The log is read up to its last whole entry: an append cut short, or bytes gone wrong after it was written, lose
// the entries from there on (their files are read again) and never give an entry that was not written. A log cut
// before its first entry is an empty one; one that starts with anything else is not a log.
func TestIndexLogStopsAtTheFirstBadEntry(t *testing.T) {
	rng := rand.New(rand.NewPCG(1273, 5))
	docs, _ := testDocs(rng, "chats", 9)
	raw := []byte(indexLogMagic)
	ends := []int{len(raw)}
	for i := range docs {
		raw = encodeLogEntry(raw, &docs[i])
		ends = append(ends, len(raw))
	}
	path := filepath.Join(t.TempDir(), "log-000001.log")
	whole := func(n int) int { // the entries wholly within the first n bytes
		return sort.SearchInts(ends, n+1) - 1
	}
	check := func(what string, data []byte, entries int) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		got, valid, _, err := readIndexLog(path)
		if entries < 0 {
			if err == nil || !damaged(err) {
				t.Fatalf("%s: read %d entries (%v), want it refused", what, len(got), err)
			}
			return
		}
		wantValid := int64(ends[entries])
		if len(data) < len(indexLogMagic) {
			wantValid = 0 // nothing whole, not even its first bytes: written from the start
		}
		if err != nil || len(got) != entries || valid != wantValid {
			t.Fatalf("%s: %d entries, %d valid bytes (%v); want %d and %d", what, len(got), valid, err, entries, wantValid)
		}
		for i := range entries {
			if d := got[docs[i].name]; d == nil || !reflect.DeepEqual(*d, docs[i]) {
				t.Fatalf("%s: entry %d read back as %+v, written as %+v", what, i, d, docs[i])
			}
		}
	}
	check("whole", raw, len(docs))
	for n := range len(raw) {
		check(fmt.Sprintf("cut to %d bytes", n), raw[:n], max(0, whole(n)))
	}
	check("not a log", []byte("PKB"+"x"), -1)
	for i := range raw {
		damaged := bytes.Clone(raw)
		damaged[i] ^= 0x10
		check(fmt.Sprintf("byte %d flipped", i), damaged, whole(i))
	}
	if got, valid, _, err := readIndexLog(filepath.Join(t.TempDir(), "none.log")); err != nil || len(got) != 0 || valid != 0 {
		t.Fatalf("no log file: %d entries, %d bytes, %v", len(got), valid, err)
	}
}

// indexArchive is a small archive with every kind of file a search reads, all old enough to be indexed: chats with
// days, a person, a project, a doc, a recording (whose meta.json and transcript.md searches do not read from disk)
// and a Moments post.
func indexArchive(t *testing.T) *Engine {
	t.Helper()
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: st}
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for c := range 4 {
		ch := &Chat{ID: fmt.Sprintf("lark-c%d", c), Name: fmt.Sprintf("群聊 %d", c), Kind: "group"}
		if err := st.PutChat(ch); err != nil {
			t.Fatal(err)
		}
		if err := st.PutSummary(KindChat, ch.ID, "# "+ch.Name+"\n\n讨论发布计划 release plan\n"); err != nil {
			t.Fatal(err)
		}
		for d := range 6 {
			day := start.AddDate(0, 0, d).Format("2006-01-02")
			text := fmt.Sprintf("# %s\n- 09:00 同事%d: 今天的进度同步一下 build ok #%d\n", day, c, d)
			if c == 1 && d == 2 {
				text += "- 09:30 同事1: Quarterly 周报 draft is ready\n"
			}
			matchesWrite(t, st.path(KindChat, ch.ID, "messages", day+".md"), text)
		}
	}
	if err := st.PutPerson(&Person{ID: "lark-u1", Name: "同事 1", Note: "负责后端 backend"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProject(&Project{Slug: "p1", Name: "项目 1", Kind: "short", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutItem(&Item{Kind: KindDoc, ID: "d1", Title: "设计文档 design", Note: "周报 template", At: start}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := e.PutRecording(recID, recUpload(1, now.Add(-time.Hour), true, "weekly sync", "周报 review"), now); err != nil {
		t.Fatal(err)
	}
	matchesWrite(t, filepath.Join(st.Dir, "moments", "posts", "m1.md"), "a post about the 周报\n")
	matchesWrite(t, filepath.Join(st.Dir, "moments", "posts", "m1.json"), `{"id":"m1"}`+"\n")
	if err := ageFiles(st.Dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	return e
}

// writeOld writes a file of the archive and sets its mtime back by age (an hour and more is old enough to index).
func writeOld(t *testing.T, e *Engine, rel, text string, age time.Duration) {
	t.Helper()
	p := filepath.Join(e.Store.Dir, filepath.FromSlash(rel))
	matchesWrite(t, p, text)
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func updateIndex(t *testing.T, e *Engine) SearchIndexStats {
	t.Helper()
	stats, err := e.UpdateSearchIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

// indexSizesFor sets the sizes an update works in for one test.
func indexSizesFor(t *testing.T, change func()) {
	t.Helper()
	was := indexSizes
	t.Cleanup(func() { indexSizes = was })
	change()
}

// smallSegments makes every update write segments of a few files, so a small archive goes through batches, more
// segments than are kept, and the merge.
func smallSegments(t *testing.T) {
	t.Helper()
	indexSizesFor(t, func() {
		indexSizes.logFiles, indexSizes.batchFiles, indexSizes.maxSegs, indexSizes.readChunk = 3, 5, 3, 4
	})
}

// allSegKeys are a segment's keys, first to last.
func allSegKeys(t *testing.T, seg *indexSeg) []segKey {
	t.Helper()
	f, err := os.Open(seg.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	keys, err := seg.readKeys(f, 0, len(seg.first), nil)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// segKeys are the keys of every file of a segment, read back from its lists.
func segKeys(t *testing.T, seg *indexSeg) [][]uint64 {
	t.Helper()
	cur, err := seg.cursor()
	if err != nil {
		t.Fatal(err)
	}
	defer cur.close()
	out := make([][]uint64, len(seg.docs))
	var ids []uint32
	for {
		key, ok, err := cur.key()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return out
		}
		if ids, err = cur.take(ids[:0]); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			out[id] = append(out[id], key)
		}
	}
}

// indexHolds is what the index on disk holds, read by a new store: every entry by path (the one searches use).
func indexHolds(t *testing.T, dir string) map[string]builtDoc {
	t.Helper()
	snap, _, _, _, err := readIndex(filepath.Join(dir, SearchIndexDir))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]builtDoc{}
	keys := make([][][]uint64, len(snap.segs))
	for i, seg := range snap.segs {
		keys[i] = segKeys(t, seg)
	}
	for name, r := range snap.where {
		out[name] = builtDoc{segDoc: snap.segs[r.seg].docs[r.id], keys: keys[r.seg][r.id]}
	}
	for name, d := range snap.log {
		out[name] = *d
	}
	return out
}

// indexNames are all the paths the index's files name, the entries searches no longer use too: what is on disk of
// each file of the archive.
func indexNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	snap, _, _, _, err := readIndex(filepath.Join(dir, SearchIndexDir))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, seg := range snap.segs {
		for _, d := range seg.docs {
			out[d.name] = true
		}
	}
	for name := range snap.log {
		out[name] = true
	}
	return out
}

// checkIndexCurrent: the index holds every file a search reads from disk, as it is now, with the keys of its text,
// and nothing else.
func checkIndexCurrent(t *testing.T, e *Engine) {
	t.Helper()
	holds := indexHolds(t, e.Store.Dir)
	want := 0
	for _, d := range e.indexFiles() {
		want++
		raw, err := os.ReadFile(d.path)
		if err != nil {
			t.Fatal(err)
		}
		st := statFile(d.path)
		got, ok := holds[d.rel]
		if !ok {
			t.Fatalf("%s is not in the index", d.rel)
		}
		if got.mod != st.mod || got.size != st.size {
			t.Fatalf("%s: the index has it as (%d, %d), it is (%d, %d)", d.rel, got.mod, got.size, st.mod, st.size)
		}
		if keys := fileGrams(fold(string(raw))); !slices.Equal(got.keys, keys) {
			t.Fatalf("%s: the index has %d keys, its text gives %d", d.rel, len(got.keys), len(keys))
		}
	}
	if want == 0 || len(holds) != want {
		names := make([]string, 0, len(holds))
		for name := range holds {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Fatalf("the index holds %d files, searches read %d: %v", len(holds), want, names)
	}
}

// checkIndexFiles: the index's directory and files are the owner's alone, and only the files the manifest names
// are there.
func checkIndexFiles(t *testing.T, e *Engine) {
	t.Helper()
	dir := e.Store.searchDir()
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%s: mode %v (%v), want 0700", SearchIndexDir, fi.Mode().Perm(), err)
	}
	_, man, _, _, err := readIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{indexManifest: true}
	for _, s := range man.Segments {
		named[s] = true
	}
	if man.Log != "" {
		named[man.Log] = true
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ents {
		fi, err := d.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !named[d.Name()] {
			t.Errorf("%s/%s is not named by the manifest", SearchIndexDir, d.Name())
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s/%s: mode %v, want 0600", SearchIndexDir, d.Name(), fi.Mode().Perm())
		}
	}
}

func TestUpdateSearchIndexReadsEveryFileSearched(t *testing.T) {
	for _, mode := range []string{"log", "segments"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "segments" {
				smallSegments(t)
			}
			e := indexArchive(t)
			if e.Store.SearchIndexReady() {
				t.Fatal("ready before any update")
			}
			stats := updateIndex(t, e)
			files := len(e.indexFiles())
			if stats.Files != files || stats.Read != files || stats.Waiting != 0 {
				t.Fatalf("first update: %+v, want all %d files read", stats, files)
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
			if !e.Store.SearchIndexReady() {
				t.Fatal("not ready after an update that finished")
			}
			holds := indexHolds(t, e.Store.Dir)
			for _, not := range []string{"recordings/" + recID + "/meta.json", "recordings/" + recID + "/transcript.md", "moments/posts/m1.json"} {
				if _, ok := holds[not]; ok {
					t.Errorf("%s is in the index; no search reads it from disk", not)
				}
			}
			if _, ok := holds["moments/posts/m1.md"]; !ok {
				t.Error("the Moments post is not in the index")
			}
			// Nothing changed: nothing is read, and a store that opens the index finds it ready.
			if stats := updateIndex(t, e); stats.Read != 0 || stats.Files != files {
				t.Fatalf("second update: %+v, want nothing read", stats)
			}
			again, err := NewStore(e.Store.Dir)
			if err != nil {
				t.Fatal(err)
			}
			if !again.SearchIndexReady() {
				t.Fatal("a new store does not find the index ready")
			}
			if stats := updateIndex(t, &Engine{Store: again}); stats.Read != 0 {
				t.Fatalf("a new store's update: %+v, want nothing read", stats)
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
		})
	}
}

// Files changed, added, renamed and deleted between updates: the next update reads the ones that are new to it and
// drops the others.
func TestUpdateSearchIndexFollowsTheFiles(t *testing.T) {
	for _, mode := range []string{"log", "segments"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "segments" {
				smallSegments(t)
			}
			e := indexArchive(t)
			updateIndex(t, e)
			day := func(c int, d string) string { return fmt.Sprintf("chats/lark-c%d/messages/%s.md", c, d) }
			// Changed: longer; another text of the same length; added; a chat renamed; a day deleted.
			writeOld(t, e, day(0, "2026-03-01"), "# 2026-03-01\n- 09:00 同事0: 改了 changed the plan\n", 50*time.Minute)
			same := "# 2026-03-02\n- 09:00 同事0: 今天的进度同步一下 buiId ok #1\n"
			if raw, err := os.ReadFile(filepath.Join(e.Store.Dir, day(0, "2026-03-02"))); err != nil || len(raw) != len(same) {
				t.Fatalf("the same-size text is %d bytes, the day %d (%v)", len(same), len(raw), err)
			}
			writeOld(t, e, day(0, "2026-03-02"), same, 50*time.Minute)
			writeOld(t, e, day(0, "2026-04-01"), "# 2026-04-01\n- 09:00 同事0: 新的一天 brand new\n", 50*time.Minute)
			if err := os.Rename(e.Store.path(KindChat, "lark-c2"), e.Store.path(KindChat, "lark-c9")); err != nil {
				t.Fatal(err)
			}
			gone := day(3, "2026-03-04")
			if err := os.Remove(filepath.Join(e.Store.Dir, gone)); err != nil {
				t.Fatal(err)
			}
			e.Store.SearchIndexPurge()
			stats := updateIndex(t, e)
			// Read: the two changed days, the new one, and the renamed chat's meta.json, summary.md and six days.
			if stats.Read != 3+8 {
				t.Fatalf("update after the changes: %+v, want 11 files read", stats)
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
			// Deleted on purpose: nothing of the deleted and renamed files is left on disk, in any segment or the log.
			for name := range indexNames(t, e.Store.Dir) {
				if name == gone || strings.HasPrefix(name, "chats/lark-c2/") {
					t.Errorf("the index still names %s", name)
				}
			}
			if stats := updateIndex(t, e); stats.Read != 0 || stats.Merged {
				t.Fatalf("update with nothing changed: %+v", stats)
			}
		})
	}
}

// indexEverHolds reports whether any entry on disk, in a segment or the log, the replaced ones too, says the file
// at name has key.
func indexEverHolds(t *testing.T, dir, name string, key uint64) bool {
	t.Helper()
	snap, man, _, _, err := readIndex(filepath.Join(dir, SearchIndexDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range snap.segs {
		keys := segKeys(t, seg)
		for id, d := range seg.docs {
			if d.name == name && slices.Contains(keys[id], key) {
				return true
			}
		}
	}
	if man.Log == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, SearchIndexDir, man.Log))
	if err != nil {
		t.Fatal(err)
	}
	for raw = raw[len(indexLogMagic):]; len(raw) >= 8; {
		n := int(uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24)
		if n == 0 || 8+n > len(raw) {
			break
		}
		if d, ok := decodeLogEntry(raw[8 : 8+n]); ok && d.name == name && slices.Contains(d.keys, key) {
			return true
		}
		raw = raw[8+n:]
	}
	return false
}

// Text deleted on purpose leaves the index at the next update: the entries of deleted files, and what the index
// read of a file before lines were taken out of it, in a segment or still in the log.
func TestSearchIndexPurgeDropsWhatTheFilesNoLongerSay(t *testing.T) {
	for _, mode := range []string{"log", "segments"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "segments" {
				smallSegments(t)
			}
			e := indexArchive(t)
			day := "chats/lark-c0/messages/2026-03-03.md"
			gone := "chats/lark-c2/messages/2026-03-03.md"
			secret := wordClause("zqsecret").all[0]
			writeOld(t, e, day, "# 2026-03-03\n- 09:00 同事0: the zqsecret plan\n- 09:01 同事0: build ok\n", 50*time.Minute)
			writeOld(t, e, gone, "# 2026-03-03\n- 09:00 同事2: zqsecret too\n", 50*time.Minute)
			updateIndex(t, e)
			// The line is taken out of one day (as retention does), the other day deleted; without the purge the
			// index still has what it read of both.
			writeOld(t, e, day, "# 2026-03-03\n- 09:01 同事0: build ok\n", 40*time.Minute)
			if err := os.Remove(filepath.Join(e.Store.Dir, gone)); err != nil {
				t.Fatal(err)
			}
			if stats := updateIndex(t, e); stats.Merged {
				t.Fatalf("update without the purge: %+v, want no merge", stats)
			}
			if !indexEverHolds(t, e.Store.Dir, day, secret) || !indexEverHolds(t, e.Store.Dir, gone, secret) {
				t.Fatal("the old entries are gone already: the test no longer shows the purge dropping them")
			}
			if got := e.Search("zqsecret", true, 10); len(got) != 0 {
				t.Fatalf("the deleted text is found: %+v", got)
			}
			e.Store.SearchIndexPurge()
			if stats := updateIndex(t, e); !stats.Merged {
				t.Fatalf("update after the purge: %+v, want a merge", stats)
			}
			if indexEverHolds(t, e.Store.Dir, day, secret) || indexEverHolds(t, e.Store.Dir, gone, secret) {
				t.Fatal("the index still has the deleted text's key")
			}
			if indexNames(t, e.Store.Dir)[gone] {
				t.Fatal("the index still names the deleted file")
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
			// With nothing to drop a purge costs nothing.
			e.Store.SearchIndexPurge()
			if stats := updateIndex(t, e); stats.Merged || stats.Read != 0 {
				t.Fatalf("a purge with nothing to drop: %+v", stats)
			}
		})
	}
}

// The engine's deletions ask for the purge themselves: a forgotten source, retention, a deleted recording.
func TestDeletionsPurgeTheSearchIndex(t *testing.T) {
	e := indexArchive(t)
	updateIndex(t, e)
	purge := &e.Store.search.purge
	if purge.Load() {
		t.Fatal("a purge is asked for before anything was deleted")
	}
	if ok, err := e.DeleteRecording(recID, time.Now()); err != nil || !ok {
		t.Fatalf("DeleteRecording: %v, %v", ok, err)
	}
	if !purge.Swap(false) {
		t.Error("deleting a recording did not ask for the purge")
	}
	if _, err := e.ForgetSource("lark"); err != nil {
		t.Fatal(err)
	}
	if !purge.Swap(false) {
		t.Error("forgetting a source did not ask for the purge")
	}
	// Retention: days older than a year go.
	writeOld(t, e, "chats/lark-c0/messages/2020-01-01.md", "# 2020-01-01\n- 09:00 同事0: long ago\n", time.Hour)
	if n := e.Prune(time.Now()); n == 0 {
		t.Fatal("retention removed nothing: the test no longer reaches the purge")
	}
	if !purge.Swap(false) {
		t.Error("retention did not ask for the purge")
	}
	if n := e.Prune(time.Now()); n != 0 || purge.Load() {
		t.Errorf("retention with nothing to remove: removed %d, purge %v", n, purge.Load())
	}
}

// Entries of files that went away without the index being told are dropped within indexPurgeEvery of the last
// merge, and entries of files changed since within indexMergeEvery.
func TestUpdateSearchIndexDropsOldEntriesInTime(t *testing.T) {
	e := indexArchive(t)
	updateIndex(t, e)
	gone := "chats/lark-c3/messages/2026-03-04.md"
	if err := os.Remove(filepath.Join(e.Store.Dir, gone)); err != nil {
		t.Fatal(err)
	}
	changed := "chats/lark-c0/messages/2026-03-01.md"
	writeOld(t, e, changed, "# 2026-03-01\n- 09:00 同事0: 改了\n", 50*time.Minute)
	if stats := updateIndex(t, e); stats.Merged || stats.Read != 1 {
		t.Fatalf("update right after: %+v, want one file read and no merge", stats)
	}
	if !indexNames(t, e.Store.Dir)[gone] {
		t.Fatal("the deleted file's entry is gone already: the test no longer shows the merge dropping it")
	}
	// merged sets back when the index was last merged, as a new process finds it.
	merged := func(ago time.Duration) *Engine {
		t.Helper()
		ix := &e.Store.search
		man := ix.man
		man.Merged = time.Now().Add(-ago)
		u := &indexUpdate{e: e, ix: ix, dir: e.Store.searchDir(), man: man}
		if err := u.publish(ix.snap.Load(), ix.logLen); err != nil {
			t.Fatal(err)
		}
		st, err := NewStore(e.Store.Dir)
		if err != nil {
			t.Fatal(err)
		}
		return &Engine{Store: st}
	}
	e = merged(indexPurgeEvery + time.Minute)
	if stats := updateIndex(t, e); !stats.Merged {
		t.Fatalf("an hour after the last merge: %+v, want a merge", stats)
	}
	if indexNames(t, e.Store.Dir)[gone] {
		t.Fatal("the deleted file's entry is still on disk after the merge")
	}
	checkIndexCurrent(t, e)
	checkIndexFiles(t, e)
	// A changed file's old entry (in a segment, the new one in the log) waits for the daily merge.
	writeOld(t, e, changed, "# 2026-03-01\n- 09:00 同事0: 又改了 again\n", 40*time.Minute)
	if stats := updateIndex(t, e); stats.Merged {
		t.Fatalf("a changed file alone: %+v, want no merge", stats)
	}
	e = merged(indexPurgeEvery + time.Minute)
	if stats := updateIndex(t, e); stats.Merged {
		t.Fatalf("a changed file an hour after the merge: %+v, want no merge yet", stats)
	}
	e = merged(indexMergeEvery + time.Minute)
	if stats := updateIndex(t, e); !stats.Merged || stats.Dropped != 1 {
		t.Fatalf("a changed file a day after the merge: %+v, want a merge dropping one entry", stats)
	}
	checkIndexCurrent(t, e)
	checkIndexFiles(t, e)
}

// A file changed in the last seconds, or too large, is left for searches to read; it is indexed once it is old
// enough.
func TestUpdateSearchIndexLeavesFreshAndHugeFiles(t *testing.T) {
	e := indexArchive(t)
	updateIndex(t, e)
	fresh := "chats/lark-c0/messages/2026-04-02.md"
	writeOld(t, e, fresh, "# 2026-04-02\n- 09:00 同事0: 刚写的 just now\n", 0)
	huge := "chats/lark-c0/messages/2026-04-03.md"
	writeOld(t, e, huge, "# 2026-04-03\n"+strings.Repeat("- 09:00 同事0: 很长 long\n", indexMaxFile/20), time.Hour)
	stats := updateIndex(t, e)
	if stats.Read != 0 || stats.Waiting != 2 {
		t.Fatalf("update: %+v, want two files waiting and none read", stats)
	}
	holds := indexHolds(t, e.Store.Dir)
	if _, ok := holds[fresh]; ok {
		t.Error("the file written just now is in the index")
	}
	if _, ok := holds[huge]; ok {
		t.Error("the file over indexMaxFile is in the index")
	}
	if !e.Store.SearchIndexReady() {
		t.Error("the index is not ready with only files it leaves out")
	}
	at := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(e.Store.Dir, fresh), at, at); err != nil {
		t.Fatal(err)
	}
	if stats := updateIndex(t, e); stats.Read != 1 || stats.Waiting != 1 {
		t.Fatalf("update once the file is old: %+v, want it read", stats)
	}
}

// stopAfter is a context that ends once its Err has been asked n times.
type stopAfter struct {
	context.Context
	left atomic.Int32
}

func (c *stopAfter) Err() error {
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// An update stopped half way keeps what it read: the index is not ready, says nothing wrong, and the next update
// reads only the rest.
func TestUpdateSearchIndexGoesOnAfterAStop(t *testing.T) {
	for _, mode := range []string{"log", "segments"} {
		t.Run(mode, func(t *testing.T) {
			smallSegments(t)
			if mode == "log" {
				indexSizesFor(t, func() { indexSizes.logFiles = 4096 })
			}
			e := indexArchive(t)
			files := len(e.indexFiles())
			ctx := &stopAfter{Context: context.Background()}
			ctx.left.Store(3)
			stats, err := e.UpdateSearchIndex(ctx)
			if !errors.Is(err, context.Canceled) || stats.Read == 0 || stats.Read >= files {
				t.Fatalf("stopped update: %+v, %v; want some of %d files read", stats, err, files)
			}
			if e.Store.SearchIndexReady() {
				t.Fatal("ready after an update that was stopped")
			}
			first := stats.Read
			if len(indexHolds(t, e.Store.Dir)) != first {
				t.Fatalf("the index holds %d files, the stopped update read %d", len(indexHolds(t, e.Store.Dir)), first)
			}
			// A new process goes on from what is on disk.
			st, err := NewStore(e.Store.Dir)
			if err != nil {
				t.Fatal(err)
			}
			e = &Engine{Store: st}
			if st.SearchIndexReady() {
				t.Fatal("a new store finds the half-built index ready")
			}
			if stats := updateIndex(t, e); stats.Read != files-first {
				t.Fatalf("next update: %+v, want the other %d files read", stats, files-first)
			}
			if !st.SearchIndexReady() {
				t.Fatal("not ready after the update that finished")
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
		})
	}
}

// An index that does not check out is started again from the files: a manifest that is not one, one written with
// other tables or another format, a segment damaged or missing, a log that is not one.
func TestUpdateSearchIndexStartsAgainWhenDamaged(t *testing.T) {
	flip := func(t *testing.T, p string, at func(n int) int) {
		t.Helper()
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		raw[at(len(raw))] ^= 0x40
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := func(t *testing.T, e *Engine) (string, indexManifestFile) {
		t.Helper()
		_, man, _, _, err := readIndex(e.Store.searchDir())
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Join(e.Store.searchDir(), indexManifest), man
	}
	for name, damage := range map[string]func(t *testing.T, e *Engine){
		"manifest is not JSON": func(t *testing.T, e *Engine) {
			p, _ := manifest(t, e)
			matchesWrite(t, p, "{")
		},
		"manifest of another type": func(t *testing.T, e *Engine) {
			p, _ := manifest(t, e)
			matchesWrite(t, p, `{"format":"one"}`)
		},
		"other tables": func(t *testing.T, e *Engine) {
			p, _ := manifest(t, e)
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			matchesWrite(t, p, strings.Replace(string(raw), indexTables, "0.0.0/0.0.0", 1))
		},
		"another format": func(t *testing.T, e *Engine) {
			p, _ := manifest(t, e)
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			matchesWrite(t, p, strings.Replace(string(raw), `"format":1`, `"format":2`, 1))
		},
		"segment's footer damaged": func(t *testing.T, e *Engine) {
			_, man := manifest(t, e)
			flip(t, filepath.Join(e.Store.searchDir(), man.Segments[0]), func(n int) int { return n - 9 })
		},
		"segment cut short": func(t *testing.T, e *Engine) {
			_, man := manifest(t, e)
			p := filepath.Join(e.Store.searchDir(), man.Segments[0])
			if err := os.Truncate(p, 100); err != nil {
				t.Fatal(err)
			}
		},
		"segment missing": func(t *testing.T, e *Engine) {
			_, man := manifest(t, e)
			if err := os.Remove(filepath.Join(e.Store.searchDir(), man.Segments[0])); err != nil {
				t.Fatal(err)
			}
		},
		"log is not a log": func(t *testing.T, e *Engine) {
			_, man := manifest(t, e)
			flip(t, filepath.Join(e.Store.searchDir(), man.Log), func(int) int { return 2 })
		},
		"manifest names a file elsewhere": func(t *testing.T, e *Engine) {
			p, man := manifest(t, e)
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			matchesWrite(t, p, strings.Replace(string(raw), man.Segments[0], "../"+man.Segments[0], 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			// A segment and a log: most files in the first, a day changed afterwards in the second.
			indexSizesFor(t, func() { indexSizes.logFiles = 8 })
			e := indexArchive(t)
			updateIndex(t, e)
			writeOld(t, e, "chats/lark-c0/messages/2026-03-01.md", "# 2026-03-01\n- 09:00 同事0: 改了\n", 50*time.Minute)
			updateIndex(t, e)
			if _, man := manifest(t, e); len(man.Segments) != 1 || man.Log == "" {
				t.Fatalf("the index is %+v, want one segment and a log", man)
			}
			damage(t, e)
			st, err := NewStore(e.Store.Dir)
			if err != nil {
				t.Fatal(err)
			}
			e = &Engine{Store: st}
			if st.SearchIndexReady() {
				t.Fatal("the damaged index is ready")
			}
			if f := e.searchFilter(words("build")); f != nil {
				t.Fatal("the damaged index answers a search")
			}
			files := len(e.indexFiles())
			stats := updateIndex(t, e)
			if !stats.Rebuilt || stats.Read != files {
				t.Fatalf("update: %+v, want the index started again and all %d files read", stats, files)
			}
			if !st.SearchIndexReady() || e.searchFilter(words("build")) == nil {
				t.Fatal("the index built again is not used")
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
		})
	}
}

// The index removed while the process that writes it runs (rm -r .search is how a person starts it again): searches
// give what the files say meanwhile, and the next update builds it again from every file. The same when only its
// log is removed: the update that would append to it finds it gone, and the one after starts again.
func TestUpdateSearchIndexStartsAgainWhenRemoved(t *testing.T) {
	for _, mode := range []string{"log", "segments"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "segments" {
				smallSegments(t)
			}
			e := indexArchive(t)
			updateIndex(t, e)
			want := findReference(e, "周报", 1<<20)
			if len(want) == 0 {
				t.Fatal("the word is not found")
			}
			if err := os.RemoveAll(e.Store.searchDir()); err != nil {
				t.Fatal(err)
			}
			if got := e.Find("周报", 1<<20); !reflect.DeepEqual(got, want) {
				t.Fatalf("Find with the index removed:\n got %+v\nwant %+v", got, want)
			}
			if got := e.Find("nowhere", 80); len(got) != 0 {
				t.Fatalf("Find of an absent word with the index removed: %+v", got)
			}
			files := len(e.indexFiles())
			if stats := updateIndex(t, e); stats.Read != files {
				t.Fatalf("update after the index was removed: %+v, want all %d files read", stats, files)
			}
			if !e.Store.SearchIndexReady() {
				t.Fatal("the index built again is not ready")
			}
			checkIndexCurrent(t, e)
			checkIndexFiles(t, e)
		})
	}
	t.Run("only the log", func(t *testing.T) {
		e := indexArchive(t)
		updateIndex(t, e)
		_, man, _, _, err := readIndex(e.Store.searchDir())
		if err != nil || man.Log == "" || len(man.Segments) != 0 {
			t.Fatalf("the index is %+v (%v), want only a log", man, err)
		}
		if err := os.Remove(filepath.Join(e.Store.searchDir(), man.Log)); err != nil {
			t.Fatal(err)
		}
		writeOld(t, e, "chats/lark-c0/messages/2026-03-01.md", "# 2026-03-01\n- 09:00 同事0: 改了\n", 50*time.Minute)
		if _, err := e.UpdateSearchIndex(context.Background()); err == nil || !damaged(err) {
			t.Fatalf("appending to a log that is gone: %v, want it found damaged", err)
		}
		if got, want := e.Find("改了", 80), findReference(e, "改了", 80); len(want) != 1 || !reflect.DeepEqual(got, want) {
			t.Fatalf("Find after the log was removed:\n got %+v\nwant %+v", got, want)
		}
		files := len(e.indexFiles())
		if stats := updateIndex(t, e); !stats.Rebuilt || stats.Read != files {
			t.Fatalf("the update after: %+v, want the index started again and all %d files read", stats, files)
		}
		checkIndexCurrent(t, e)
		checkIndexFiles(t, e)
	})
}

// A list that fails its check when a search reads it: the search goes without the index, which is then built again.
func TestSearchFilterRefusesADamagedList(t *testing.T) {
	smallSegments(t)
	e := indexArchive(t)
	updateIndex(t, e)
	_, man, _, _, err := readIndex(e.Store.searchDir())
	if err != nil || len(man.Segments) != 1 {
		t.Fatalf("the index is %+v (%v), want one segment", man, err)
	}
	p := filepath.Join(e.Store.searchDir(), man.Segments[0])
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Every list damaged (their first bytes), the files and keys left whole: the segment opens.
	seg, err := openSeg(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range allSegKeys(t, seg) {
		raw[seg.lists+int64(k.start)] ^= 0x80
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	e = &Engine{Store: st, Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }}
	if !st.SearchIndexReady() {
		t.Fatal("the segment did not open: the test no longer reaches a list's check")
	}
	if f := e.searchFilter(words("build")); f != nil {
		t.Fatal("a damaged list was believed")
	}
	if st.SearchIndexReady() || e.searchFilter(words("周报")) != nil {
		t.Fatal("the index is still used after a list failed its check")
	}
	if len(logged) != 1 {
		t.Fatalf("logged %q, want the failed check said once", logged)
	}
	for _, l := range logged {
		if strings.Contains(l, e.Store.Dir) || strings.Contains(l, "build") {
			t.Errorf("the log line %q names a path or the query", l)
		}
	}
	if stats := updateIndex(t, e); !stats.Rebuilt {
		t.Fatalf("update: %+v, want the index started again", stats)
	}
	checkIndexCurrent(t, e)
	if e.searchFilter(words("build")) == nil {
		t.Fatal("the index built again is not used")
	}
}

// Damage no search has read is found by the merge that reads every list: that update fails, and the next one
// starts the index again.
func TestUpdateSearchIndexFindsDamageWhenItMerges(t *testing.T) {
	smallSegments(t)
	e := indexArchive(t)
	updateIndex(t, e)
	_, man, _, _, err := readIndex(e.Store.searchDir())
	if err != nil || len(man.Segments) != 1 {
		t.Fatalf("the index is %+v (%v), want one segment", man, err)
	}
	p := filepath.Join(e.Store.searchDir(), man.Segments[0])
	seg, err := openSeg(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	keys := allSegKeys(t, seg)
	raw[seg.lists+int64(keys[len(keys)/2].start)] ^= 0x80
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// A deleted file, dropped at once: a merge.
	if err := os.Remove(filepath.Join(e.Store.Dir, "chats/lark-c3/messages/2026-03-04.md")); err != nil {
		t.Fatal(err)
	}
	e.Store.SearchIndexPurge()
	if stats, err := e.UpdateSearchIndex(context.Background()); err == nil || !damaged(err) {
		t.Fatalf("update over the damaged segment: %+v, %v; want it refused as damaged", stats, err)
	}
	if e.Store.SearchIndexReady() || e.searchFilter(words("build")) != nil {
		t.Fatal("the index is still used after its merge failed a check")
	}
	if stats := updateIndex(t, e); !stats.Rebuilt {
		t.Fatalf("next update: %+v, want the index started again", stats)
	}
	checkIndexCurrent(t, e)
	checkIndexFiles(t, e)
}

// A read-only store follows the index another process writes: it reads the manifest again once it has changed, and
// a segment merged away under it sends it back to the manifest instead of to the scan for good.
func TestReadOnlyStoreFollowsTheIndex(t *testing.T) {
	was := indexFollowEvery
	t.Cleanup(func() { indexFollowEvery = was })
	indexFollowEvery = 0
	smallSegments(t)
	e := indexArchive(t)
	updateIndex(t, e)
	ro, err := OpenReadOnly(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	reader := &Engine{Store: ro}
	day := "chats/lark-c0/messages/2026-04-09.md"
	holds := func() bool {
		f := reader.searchFilter(words("zqfollow"))
		if f == nil {
			t.Fatal("the read-only store has no index to ask")
		}
		st := statFile(filepath.Join(e.Store.Dir, day))
		// The day is one the index read as it is now, and it holds the word: not ruled out, and not unknown.
		_, inLog := f.snap.log[day]
		_, inSeg := f.snap.where[day]
		return st.ok && (inLog || inSeg) && !f.skip(day, st)
	}
	if reader.searchFilter(words("build")) == nil {
		t.Fatal("the read-only store does not use the index")
	}
	writeOld(t, e, day, "# 2026-04-09\n- 09:00 同事0: zqfollow\n", 50*time.Minute)
	if err := os.Remove(filepath.Join(e.Store.Dir, "chats/lark-c3/messages/2026-03-04.md")); err != nil {
		t.Fatal(err)
	}
	e.Store.SearchIndexPurge()
	if stats := updateIndex(t, e); !stats.Merged {
		t.Fatalf("update: %+v, want a merge", stats)
	}
	if !holds() {
		t.Fatal("the read-only store does not have the day the writer indexed")
	}
	// Without looking at the manifest: the segments it knew are gone, which it finds when it reads one.
	indexFollowEvery = time.Hour
	ro.search.looked.Store(time.Now().UnixNano())
	writeOld(t, e, day, "# 2026-04-09\n- 09:00 同事0: zqfollow again\n", 40*time.Minute)
	if err := os.Remove(filepath.Join(e.Store.Dir, "chats/lark-c3/messages/2026-03-05.md")); err != nil {
		t.Fatal(err)
	}
	e.Store.SearchIndexPurge()
	if stats := updateIndex(t, e); !stats.Merged {
		t.Fatalf("second update: %+v, want a merge", stats)
	}
	if !holds() {
		t.Fatal("the read-only store did not read the index again after its segment was merged away")
	}
	if got, want := reader.Search("zqfollow", true, 10), searchByWalk(reader, "zqfollow", true, 10); !reflect.DeepEqual(got, want) || len(got) != 1 {
		t.Fatalf("Search of the read-only store:\n got %+v\nwant %+v", got, want)
	}
}

// An append to the log cut short (the process died) loses only that append: what was written before is used, and
// the next update reads the rest again and leaves a whole log.
func TestUpdateSearchIndexAfterATornLog(t *testing.T) {
	for _, c := range []struct {
		name string
		cut  func(logLen int64) int64
		lost func(files int) int
	}{
		{"the last entry", func(n int64) int64 { return n - 5 }, func(int) int { return 1 }},
		// The log is not synced: a crash right after it was started can leave it with nothing, or part of its head.
		{"everything", func(int64) int64 { return 0 }, func(files int) int { return files }},
		{"all but part of its head", func(int64) int64 { return 3 }, func(files int) int { return files }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := indexArchive(t)
			updateIndex(t, e)
			_, man, logLen, _, err := readIndex(e.Store.searchDir())
			if err != nil || man.Log == "" || len(man.Segments) != 0 {
				t.Fatalf("the index is %+v (%v), want a log alone", man, err)
			}
			files := len(e.indexFiles())
			p := filepath.Join(e.Store.searchDir(), man.Log)
			if err := os.Truncate(p, c.cut(logLen)); err != nil {
				t.Fatal(err)
			}
			st, err := NewStore(e.Store.Dir)
			if err != nil {
				t.Fatal(err)
			}
			e = &Engine{Store: st}
			lost := c.lost(files)
			if got := len(indexHolds(t, st.Dir)); got != files-lost {
				t.Fatalf("the torn log gives %d files, want %d", got, files-lost)
			}
			if stats := updateIndex(t, e); stats.Read != lost || stats.Rebuilt {
				t.Fatalf("update: %+v, want the %d lost file(s) read and nothing started again", stats, lost)
			}
			checkIndexCurrent(t, e)
			if _, _, valid, _, err := readIndex(st.searchDir()); err != nil {
				t.Fatal(err)
			} else if fi, err := os.Stat(p); err != nil || fi.Size() != valid {
				t.Fatalf("the log is %d bytes, %d of them whole entries (%v)", fi.Size(), valid, err)
			}
		})
	}
}

// A read-only store uses the index that is there and never writes one.
func TestSearchIndexOfAReadOnlyStore(t *testing.T) {
	e := indexArchive(t)
	ro, err := OpenReadOnly(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&Engine{Store: ro}).UpdateSearchIndex(context.Background()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("update of a read-only store: %v, want ErrReadOnly", err)
	}
	if ro.SearchIndexReady() || (&Engine{Store: ro}).searchFilter(words("build")) != nil {
		t.Fatal("a read-only store without an index has one")
	}
	if _, err := os.Stat(e.Store.searchDir()); !os.IsNotExist(err) {
		t.Fatalf("a read-only store made %s: %v", SearchIndexDir, err)
	}
	updateIndex(t, e)
	if ro, err = OpenReadOnly(e.Store.Dir); err != nil {
		t.Fatal(err)
	}
	if !ro.SearchIndexReady() || (&Engine{Store: ro}).searchFilter(words("build")) == nil {
		t.Fatal("a read-only store does not use the index that is there")
	}
}
