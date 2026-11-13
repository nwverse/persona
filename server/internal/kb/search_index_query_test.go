package kb

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
)

// The searches with a search index give what they give without one, whatever state the index is in: these tests
// compare Find, Search, SearchKind and Matches with the scans they replaced (findReference, searchByWalk,
// matchesReadingEveryFile, which read every file themselves) over random archives and random queries.

// diffWords are what the random archive's lines are made of: Chinese and English, the two run together, both
// cases, composed and decomposed, full-width, punctuation, an emoji.
var diffWords = []string{
	"周报", "进度", "发布计划", "同步", "复盘", "的", "一下", "今天", "明天开会", "需求评审",
	"build", "Build", "BUILD", "review", "draft", "deploy", "ok", "a", "release-notes", "v1.2", "c++", "#42", "(urgent)",
	"café", "café", "ＡＢ", "进度ok", "v2发布", "review周报", "🙂", "naïve", "İstanbul", "x_y", "foo/bar",
}

func diffLine(rng *rand.Rand) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "- %02d:%02d 同事%d:", 9+rng.IntN(9), rng.IntN(60), rng.IntN(4))
	for range 1 + rng.IntN(5) {
		if rng.IntN(4) > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(diffWords[rng.IntN(len(diffWords))])
	}
	if rng.IntN(8) == 0 {
		sb.WriteString("\n  " + diffWords[rng.IntN(len(diffWords))] + " 续行 continued")
	}
	return sb.String()
}

func diffText(rng *rand.Rand, lines int) string {
	var sb strings.Builder
	for range lines {
		sb.WriteString(diffLine(rng) + "\n")
	}
	return sb.String()
}

// diffArchive is a random archive with every kind of file the searches read, old enough to be indexed.
func diffArchive(t *testing.T, seed uint64) *Engine {
	t.Helper()
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: st}
	rng := rand.New(rand.NewPCG(1273, seed))
	at := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	for c := range 6 {
		ch := &Chat{ID: fmt.Sprintf("lark-c%d", c), Name: diffWords[rng.IntN(len(diffWords))] + " 群", Kind: "group",
			Tagging: Tagging{Importance: fmt.Sprintf("P%d", c%4), Relevance: c % 3}, LastAt: at}
		if err := st.PutChat(ch); err != nil {
			t.Fatal(err)
		}
		matchesWrite(t, st.path(KindChat, ch.ID, "summary.md"), "# "+ch.Name+"\n\n"+diffText(rng, rng.IntN(4)))
		for d := range 14 {
			day := at.AddDate(0, 0, d).Format("2006-01-02")
			matchesWrite(t, st.path(KindChat, ch.ID, "messages", day+".md"), "# "+day+"\n"+diffText(rng, rng.IntN(30)))
		}
	}
	// A chat with a line of the common word on every day: far past foundCap.
	if err := st.PutChat(&Chat{ID: "lark-busy", Name: "发布群", Kind: "group", LastAt: at}); err != nil {
		t.Fatal(err)
	}
	for d := range 20 {
		day := at.AddDate(0, 0, d).Format("2006-01-02")
		text := "# " + day + "\n" + strings.Repeat("- 10:00 同事1: build ok 进度同步\n", 9) + diffText(rng, 2)
		matchesWrite(t, st.path(KindChat, "lark-busy", "messages", day+".md"), text)
	}
	for p := range 3 {
		id := fmt.Sprintf("lark-u%d", p)
		if err := st.PutPerson(&Person{ID: id, Name: "同事 " + diffWords[rng.IntN(len(diffWords))], Note: diffLine(rng)}); err != nil {
			t.Fatal(err)
		}
		matchesWrite(t, st.path(KindPerson, id, "summary.md"), diffText(rng, 3))
	}
	for p := range 2 {
		slug := fmt.Sprintf("p%d", p)
		if err := st.PutProject(&Project{Slug: slug, Name: "项目 " + diffWords[rng.IntN(len(diffWords))], Kind: "short", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		matchesWrite(t, st.path(KindProject, slug, "summary.md"), diffText(rng, 5))
		matchesWrite(t, st.path(KindProject, slug, "log.md"), diffText(rng, 12))
	}
	for i := range 4 {
		if err := st.PutItem(&Item{Kind: KindDoc, ID: fmt.Sprintf("d%d", i), Title: "文档 " + diffWords[rng.IntN(len(diffWords))], Note: diffLine(rng), At: at}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if _, err := e.PutRecording(recID, recUpload(1, now.Add(-time.Hour), true, "周报 review first", "build 进度", "café again"), now); err != nil {
		t.Fatal(err)
	}
	matchesWrite(t, filepath.Join(st.Dir, "moments", "posts", "m1.md"), diffText(rng, 4))
	matchesWrite(t, filepath.Join(st.Dir, "moments", "posts", "m2.md"), diffText(rng, 4))
	if err := ageFiles(st.Dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	return e
}

// diffQueries are random queries for the archive: runs of its files' text as written (a word, part of one, several
// words), the same in the other case, words it does not have, and the short and mixed ones the index answers in
// its own ways.
func diffQueries(t *testing.T, e *Engine, rng *rand.Rand, n int) []string {
	t.Helper()
	out := []string{"build", "周报", "周", "报", "的", "a", "ok", "bu", "度o", "报 dr", "周报 draft", "build 周报 review", "c++", "+", "#",
		"#42", "(", "café", "CAFÉ", "ａｂ", "🙂", "i̇stanbul", "İ", "nowhere", "build nowhere", "不存在", "续行", "同事1:", "10:00"}
	var texts []string
	for _, d := range e.files().all {
		if raw, err := os.ReadFile(d.path); err == nil && len(raw) > 0 {
			texts = append(texts, string(raw))
		}
	}
	if len(texts) == 0 {
		t.Fatal("the archive has no text")
	}
	for range n {
		text := []rune(texts[rng.IntN(len(texts))])
		start := rng.IntN(len(text))
		q := string(text[start:min(len(text), start+1+rng.IntN(8))])
		switch rng.IntN(6) {
		case 0:
			q = strings.ToUpper(q)
		case 1: // and a run of another file: seldom both in one
			other := []rune(texts[rng.IntN(len(texts))])
			at := rng.IntN(len(other))
			q += " " + string(other[at:min(len(other), at+1+rng.IntN(4))])
		case 2:
			q += "zq"
		}
		if strings.TrimFunc(q, unicode.IsSpace) != "" {
			out = append(out, q)
		}
	}
	return out
}

// withoutIndex runs f with the index's directory moved out of the data dir: on an engine that finds none.
func withoutIndex(t *testing.T, dir string, f func(plain *Engine)) {
	t.Helper()
	at, away := filepath.Join(dir, SearchIndexDir), filepath.Join(t.TempDir(), "index")
	moved := os.Rename(at, away) == nil
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	f(&Engine{Store: st})
	if moved {
		if err := os.Rename(away, at); err != nil {
			t.Fatal(err)
		}
	}
}

// checkSearches compares every search of e's store with the scans, for each query: on e itself (which may have
// searched before, and holds the index as the test left it) and on an engine over a store opened now.
func checkSearches(t *testing.T, step string, e *Engine, queries []string) {
	t.Helper()
	limits := []int{1, 3, 40, 1 << 20}
	kinds := []string{KindChat, KindPerson, KindProject, KindDoc, KindRecording}
	entries := [][2]string{{KindChat, "lark-c1"}, {KindChat, "lark-busy"}, {KindPerson, "lark-u0"}, {KindProject, "p0"}, {KindDoc, "d0"}, {KindRecording, recID}}
	type kindKey struct {
		q, kind  string
		messages bool
	}
	// What the same searches give with no index at all, for the one that has no scan of its own to compare with.
	byKind := map[kindKey][]Hit{}
	withoutIndex(t, e.Store.Dir, func(plain *Engine) {
		for _, q := range queries {
			for _, kind := range kinds {
				for _, messages := range []bool{true, false} {
					byKind[kindKey{q, kind, messages}] = plain.SearchKind(q, kind, messages, 1<<20)
				}
			}
		}
	})
	fresh, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	engines := map[string]*Engine{"the engine": e, "a new engine": {Store: fresh}}
	for _, q := range queries {
		ref := &Engine{Store: e.Store}
		wantFind := findReference(ref, q, 1<<30)
		wantSearch := map[bool][]Hit{true: searchByWalk(ref, q, true, 1<<20), false: searchByWalk(ref, q, false, 1<<20)}
		for name, eng := range engines {
			for _, limit := range limits {
				want := wantFind[:min(limit, len(wantFind))]
				if got := eng.Find(q, limit); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
					t.Fatalf("%s, %s: Find(%q, %d):\n got %+v\nwant %+v", step, name, q, limit, got, want)
				}
				for messages, all := range wantSearch {
					want := all[:min(limit, len(all))]
					if got := eng.Search(q, messages, limit); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
						t.Fatalf("%s, %s: Search(%q, %v, %d):\n got %d %+v\nwant %d %+v", step, name, q, messages, limit, len(got), got, len(want), want)
					}
				}
				for _, en := range entries {
					want := matchesReadingEveryFile(ref, en[0], en[1], q, limit)
					if got := eng.Matches(en[0], en[1], q, limit); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
						t.Fatalf("%s, %s: Matches(%s, %s, %q, %d):\n got %+v\nwant %+v", step, name, en[0], en[1], q, limit, got, want)
					}
				}
				for _, kind := range kinds {
					for _, messages := range []bool{true, false} {
						all := byKind[kindKey{q, kind, messages}]
						want := all[:min(limit, len(all))]
						if got := eng.SearchKind(q, kind, messages, limit); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
							t.Fatalf("%s, %s: SearchKind(%q, %s, %v, %d):\n got %d %+v\nwant %d %+v", step, name, q, kind, messages, limit, len(got), got, len(want), want)
						}
					}
				}
			}
		}
	}
}

// changeArchive changes the archive under an index: days rewritten (longer, and to another text of the same size),
// a day and a chat added, a chat renamed, a day and a chat deleted, a file written just now.
func changeArchive(t *testing.T, e *Engine, rng *rand.Rand) {
	t.Helper()
	day := func(chat, d string) string { return "chats/" + chat + "/messages/" + d + ".md" }
	writeOld(t, e, day("lark-c0", "2026-05-01"), "# 2026-05-01\n"+diffText(rng, 12)+"- 12:00 同事9: 改过的 zqchanged 周报\n", 30*time.Minute)
	same := day("lark-c0", "2026-05-02")
	raw, err := os.ReadFile(filepath.Join(e.Store.Dir, same))
	if err != nil {
		t.Fatal(err)
	}
	// The same size, mtime changed: every letter of the day becomes another one.
	swapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r < 'z':
			return r + 1
		case r == 'z':
			return 'a'
		}
		return r
	}, string(raw))
	if len(swapped) != len(raw) {
		t.Fatalf("the swapped day is %d bytes, was %d", len(swapped), len(raw))
	}
	writeOld(t, e, same, swapped, 30*time.Minute)
	writeOld(t, e, day("lark-c0", "2026-06-01"), "# 2026-06-01\n- 09:00 同事1: 新的一天 zqadded build\n", 30*time.Minute)
	if err := e.Store.PutChat(&Chat{ID: "lark-new", Name: "新群 zqnewchat", Kind: "group"}); err != nil {
		t.Fatal(err)
	}
	writeOld(t, e, day("lark-new", "2026-06-02"), "# 2026-06-02\n"+diffText(rng, 6), 30*time.Minute)
	if err := os.Rename(e.Store.path(KindChat, "lark-c2"), e.Store.path(KindChat, "lark-c7")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.Store.Dir, day("lark-c3", "2026-05-03"))); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e.Store.path(KindChat, "lark-c4")); err != nil {
		t.Fatal(err)
	}
	// Written just now, twice in the same moment: the second text has the first one's size.
	writeOld(t, e, day("lark-c5", "2026-05-04"), "# 2026-05-04\n- 09:00 同事1: zqfirst\n", 0)
	writeOld(t, e, day("lark-c5", "2026-05-04"), "# 2026-05-04\n- 09:00 同事1: zqlater\n", 0)
}

func TestSearchesWithAnIndexGiveWhatTheScanGives(t *testing.T) {
	changedQueries := []string{"zqchanged", "zqadded", "zqnewchat", "zqfirst", "zqlater", "新群", "改过的 周报"}
	for _, mode := range []string{"log", "segments"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "segments" {
				smallSegments(t)
			}
			e := diffArchive(t, 1)
			rng := rand.New(rand.NewPCG(1273, 11))
			queries := diffQueries(t, e, rng, 40)
			checkSearches(t, "no index", e, queries)

			// Half built: an update stopped after its first files.
			ctx := &stopAfter{Context: context.Background()}
			ctx.left.Store(3)
			indexSizesFor(t, func() { indexSizes.readChunk = 16 })
			if stats, err := e.UpdateSearchIndex(ctx); err == nil || stats.Read == 0 || stats.Read >= stats.Files {
				t.Fatalf("stopped update: %+v, %v", stats, err)
			}
			checkSearches(t, "half built", e, queries)

			updateIndex(t, e)
			checkIndexCurrent(t, e)
			checkSearches(t, "built", e, queries)

			// Behind: the files changed, the index not told.
			changeArchive(t, e, rng)
			queries = append(diffQueries(t, e, rng, 25), changedQueries...)
			checkSearches(t, "behind", e, queries)

			e.Store.SearchIndexPurge()
			updateIndex(t, e)
			checkSearches(t, "up to date again", e, queries)

			// Changed again and updated without the purge: entries of files gone stay in the index for a while.
			if err := os.Remove(filepath.Join(e.Store.Dir, "chats/lark-c5/messages/2026-05-05.md")); err != nil {
				t.Fatal(err)
			}
			writeOld(t, e, "chats/lark-c5/messages/2026-05-06.md", "# 2026-05-06\n- 09:00 同事1: zqchanged again 周报\n", 30*time.Minute)
			updateIndex(t, e)
			checkSearches(t, "updated with entries of files gone", e, queries)
		})
	}
}

// A damaged index is found out when it is opened or when a search reads the damaged part; either way the search
// gives what the scan gives.
func TestSearchesWithADamagedIndexGiveWhatTheScanGives(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, e *Engine, man indexManifestFile){
		"lists": func(t *testing.T, e *Engine, man indexManifestFile) {
			p := filepath.Join(e.Store.searchDir(), man.Segments[0])
			seg, err := openSeg(p)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			for i, k := range allSegKeys(t, seg) {
				if i%2 == 0 {
					raw[seg.lists+int64(k.start)] ^= 0x02
				}
			}
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"key blocks": func(t *testing.T, e *Engine, man indexManifestFile) {
			p := filepath.Join(e.Store.searchDir(), man.Segments[0])
			seg, err := openSeg(p)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			for b := range seg.first {
				raw[seg.keys+int64(seg.keyOff[b])+1] ^= 0x20
			}
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"segment gone": func(t *testing.T, e *Engine, man indexManifestFile) {
			if err := os.Remove(filepath.Join(e.Store.searchDir(), man.Segments[0])); err != nil {
				t.Fatal(err)
			}
		},
		"manifest": func(t *testing.T, e *Engine, man indexManifestFile) {
			matchesWrite(t, filepath.Join(e.Store.searchDir(), indexManifest), `{"format":1,"tables":"`)
		},
		"log": func(t *testing.T, e *Engine, man indexManifestFile) {
			p := filepath.Join(e.Store.searchDir(), man.Log)
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			for i := len(indexLogMagic) + 9; i < len(raw); i += 97 {
				raw[i] ^= 0x04
			}
			if err := os.WriteFile(p, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// A segment and a log: most of the archive in the first, two days changed afterwards in the second.
			indexSizesFor(t, func() { indexSizes.logFiles = 8 })
			e := diffArchive(t, 2)
			updateIndex(t, e)
			writeOld(t, e, "chats/lark-c0/messages/2026-05-01.md", "# 2026-05-01\n- 09:00 同事1: 周报 build zqlog\n", 30*time.Minute)
			writeOld(t, e, "chats/lark-c1/messages/2026-05-01.md", "# 2026-05-01\n- 09:00 同事2: review zqlog\n", 30*time.Minute)
			updateIndex(t, e)
			_, man, _, _, err := readIndex(e.Store.searchDir())
			if err != nil || len(man.Segments) != 1 || man.Log == "" {
				t.Fatalf("the index is %+v (%v), want one segment and a log", man, err)
			}
			rng := rand.New(rand.NewPCG(1273, 12))
			queries := append(diffQueries(t, e, rng, 25), "zqlog", "zqlog 周报")
			// e searched with the index whole before it was damaged; the damage is on disk under it.
			if got := e.Find("周报", 5); len(got) == 0 {
				t.Fatal("nothing found before the damage")
			}
			damage(t, e, man)
			checkSearches(t, "damaged", e, queries)
			// The next process finds the damage when it opens the index, and its update builds it again (a damaged
			// log is read up to the damage, and the files after it read again).
			st, err := NewStore(e.Store.Dir)
			if err != nil {
				t.Fatal(err)
			}
			e = &Engine{Store: st}
			// Damage inside a segment is found by the search that reads it.
			if got, want := e.Find("build 周报", 5), findReference(e, "build 周报", 5); !reflect.DeepEqual(got, want) {
				t.Fatalf("Find over the damaged index:\n got %+v\nwant %+v", got, want)
			}
			if stats := updateIndex(t, e); stats.Rebuilt == (name == "log") || stats.Read == 0 {
				t.Fatalf("update after the damage: %+v", stats)
			}
			checkIndexCurrent(t, e)
			checkSearches(t, "built again", e, queries)
		})
	}
}

// With an index, a search reads the files that can hold its words and no others: a word no file has reads none,
// a rare one the files that have it, and a word on every day of a chat only the newest days Find needs.
func TestSearchesReadOnlyWhatTheIndexLeaves(t *testing.T) {
	e := indexArchive(t)
	updateIndex(t, e)
	files := len(e.indexFiles())
	reads := func(f func(e *Engine)) int {
		t.Helper()
		st, err := NewStore(e.Store.Dir)
		if err != nil {
			t.Fatal(err)
		}
		cold := &Engine{Store: st}
		f(cold)
		return int(cold.find.reads.Load())
	}
	for _, c := range []struct {
		name string
		f    func(e *Engine)
		want int
	}{
		{"Find of a word no file has", func(e *Engine) { e.Find("nowhere", 80) }, 0},
		{"Search of a word no file has", func(e *Engine) { e.Search("nowhere", true, 40) }, 0},
		{"Search of two words no file has together", func(e *Engine) { e.Search("周报 backend", true, 40) }, 0},
		{"SearchKind of a word no file has", func(e *Engine) { e.SearchKind("nowhere", KindChat, true, 40) }, 0},
		{"Matches of a word no day has", func(e *Engine) { e.Matches(KindChat, "lark-c0", "nowhere", 40) }, 0},
		// 周报 is in one day of lark-c1, a doc and a Moments post (which Find does not read).
		{"Find of a rare word", func(e *Engine) { e.Find("周报", 80) }, 2},
		{"Search of a rare word", func(e *Engine) { e.Search("周报", true, 40) }, 3},
		{"Search of a rare word, one hit", func(e *Engine) { e.Search("周报 draft", true, 1) }, 1},
		{"Matches of a rare word", func(e *Engine) { e.Matches(KindChat, "lark-c1", "周报", 40) }, 1},
		{"Find of one character", func(e *Engine) { e.Find("报", 80) }, 2},
	} {
		if got := reads(c.f); got != c.want {
			t.Errorf("%s: %d files read, want %d (of %d)", c.name, got, c.want, files)
		}
	}
	// Without the index every file is read, which is what the counts above are measured against.
	if got := reads(func(e *Engine) {
		if err := os.RemoveAll(e.Store.searchDir()); err != nil {
			t.Fatal(err)
		}
		e.Find("nowhere", 80)
	}); got < files-2 {
		t.Fatalf("Find without an index read %d files of %d", got, files)
	}
}

// A chat with the word on every line of every day is settled by its newest days: Find reads those, and gives what
// reading them all gives.
func TestFindReadsTheNewestDaysOfABusyChat(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutChat(&Chat{ID: "lark-busy", Name: "发布群", Kind: "group"}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const days = 400
	for d := range days {
		day := at.AddDate(0, 0, d).Format("2006-01-02")
		var sb strings.Builder
		for l := range 10 {
			fmt.Fprintf(&sb, "- 10:%02d 同事1: build ok #%d\n", l, d*10+l)
		}
		matchesWrite(t, st.path(KindChat, "lark-busy", "messages", day+".md"), "# "+day+"\n"+sb.String())
	}
	if err := ageFiles(st.Dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, indexed := range []bool{false, true} {
		e := &Engine{Store: st}
		if indexed {
			updateIndex(t, e)
		}
		want := findReference(&Engine{Store: st}, "build", 80)
		if got := e.Find("build", 80); !reflect.DeepEqual(got, want) || len(got) != 1 || got[0].Count != foundCap {
			t.Fatalf("indexed %v: Find:\n got %+v\nwant %+v", indexed, got, want)
		}
		if got := int(e.find.reads.Load()); got > 16 {
			t.Errorf("indexed %v: %d of %d days read, want the newest 16 at most", indexed, got, days)
		}
		// A word the chat does not have is every day read without the index, none with it.
		e = &Engine{Store: st}
		if got := e.Find("nowhere", 80); len(got) != 0 {
			t.Fatalf("indexed %v: found %+v", indexed, got)
		}
		if got := int(e.find.reads.Load()); indexed && got != 0 || !indexed && got < days {
			t.Errorf("indexed %v: %d files read for a word no file has", indexed, got)
		}
	}
}

// The comparison over the benchmark's generator (find_bench_test.go), repeated and varied text, with queries taken
// from the archive at random: Find and Search give what the scans give, in order and at every limit.
func TestSearchesWithAnIndexOnTheGeneratedArchive(t *testing.T) {
	for _, varied := range []bool{false, true} {
		t.Run(fmt.Sprintf("varied %v", varied), func(t *testing.T) {
			dir := t.TempDir()
			shape := archiveShape{chats: 12, days: 40, stride: 3, lines: benchLines, start: time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), varied: varied}
			if err := fillArchive(dir, shape); err != nil {
				t.Fatal(err)
			}
			st, err := NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			e := &Engine{Store: st}
			indexSizesFor(t, func() { indexSizes.logFiles, indexSizes.batchFiles = 64, 200 })
			if stats := updateIndex(t, e); stats.Read != stats.Files || stats.Files < shape.chats*shape.days {
				t.Fatalf("update: %+v", stats)
			}
			rng := rand.New(rand.NewPCG(1273, 13))
			queries := append(diffQueries(t, e, rng, 30), "周报 draft", "Quarterly", "群聊 05", "同事 1", "设计文档", "review #7")
			checkGenerated(t, e, queries)
			// And behind: a tenth of the days rewritten, some removed, the index not told.
			days := e.files().days
			for i, d := range days {
				switch {
				case i%10 == 3:
					matchesWrite(t, d.path, "# day\n- 09:00 同事1: rewritten zqrewritten 周报 build\n")
				case i%10 == 7:
					if err := os.Remove(d.path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := ageFiles(dir, 30*time.Minute); err != nil {
				t.Fatal(err)
			}
			checkGenerated(t, e, append(queries, "zqrewritten", "rewritten 周报"))
		})
	}
}

func checkGenerated(t *testing.T, e *Engine, queries []string) {
	t.Helper()
	fresh, err := NewStore(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range queries {
		ref := &Engine{Store: e.Store}
		wantFind := findReference(ref, q, 1<<30)
		wantSearch := searchByWalk(ref, q, true, 1<<20)
		for _, eng := range []*Engine{e, {Store: fresh}} {
			for _, limit := range []int{1, 5, 80, 1 << 20} {
				want := wantFind[:min(limit, len(wantFind))]
				if got := eng.Find(q, limit); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
					t.Fatalf("Find(%q, %d):\n got %+v\nwant %+v", q, limit, got, want)
				}
				hits := wantSearch[:min(limit, len(wantSearch))]
				if got := eng.Search(q, true, limit); !reflect.DeepEqual(got, hits) && (len(got) > 0 || len(hits) > 0) {
					t.Fatalf("Search(%q, %d):\n got %d %+v\nwant %d %+v", q, limit, len(got), got, len(hits), hits)
				}
			}
		}
	}
}

// PERSONA_KB_BENCH_ARCHIVE=1 runs the comparison over the bench archive (~37k files) with its index built in
// segments: the word on every line, rare words, one character, words in names, a word in no file, random runs.
func TestSearchesWithAnIndexOnTheBenchArchive(t *testing.T) {
	if os.Getenv("PERSONA_KB_BENCH_ARCHIVE") != "1" {
		t.Skip("PERSONA_KB_BENCH_ARCHIVE not set")
	}
	e := &Engine{Store: benchStore(t)}
	// The other tests on the bench archive take it without an index.
	t.Cleanup(func() { _ = os.RemoveAll(e.Store.searchDir()) })
	updateIndex(t, e)
	rng := rand.New(rand.NewPCG(1273, 14))
	queries := append(diffQueries(t, e, rng, 12), "build", "周报 draft", "周", "群聊 05", "nowhere")
	sort.Strings(queries)
	checkGenerated(t, e, queries)
}
