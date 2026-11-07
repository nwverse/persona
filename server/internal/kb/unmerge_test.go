package kb

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// Unmerge tests. All names are placeholders.

// mergeFixture is two projects that shared a person and a chat before dedupe merged them: keep (甲) with p1, p2, c1,
// docs/d1; drop (乙, paused) with p2, p3, c1, c2, docs/d2.
func mergeFixture(t *testing.T, e *Engine) {
	t.Helper()
	put := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	put(e.Store.PutProject(&Project{Slug: "keep", Name: "示例 甲", Kind: "short", Status: "active", Aliases: []string{"甲计划"},
		People: []string{"p1", "p2"}, Chats: []string{"c1"}, Items: []string{"docs/d1"}, Dirs: []string{"m:/a"},
		Moments: []ProjectMoment{{ID: "m1", At: time.Now().Add(-time.Hour)}}}))
	put(e.Store.PutProject(&Project{Slug: "drop", Name: "示例 乙", Kind: "long", Status: "paused", Aliases: []string{"乙计划"},
		People: []string{"p2", "p3"}, Chats: []string{"c1", "c2"}, Items: []string{"docs/d2"}, Dirs: []string{"m:/b"},
		Keywords: []string{"乙"}, Moments: []ProjectMoment{{ID: "m2", At: time.Now()}}}))
	put(e.Store.PutChat(&Chat{ID: "c1", Kind: "group", Projects: []string{"keep", "drop"}}))
	put(e.Store.PutChat(&Chat{ID: "c2", Kind: "group", Projects: []string{"drop"}}))
	put(e.Store.PutPerson(&Person{ID: "p1", Projects: []string{"keep"}}))
	put(e.Store.PutPerson(&Person{ID: "p2", Projects: []string{"keep", "drop"}}))
	put(e.Store.PutPerson(&Person{ID: "p3", Projects: []string{"drop"}}))
	put(e.Store.PutItem(&Item{Kind: KindDoc, ID: "d1", Projects: []string{"keep"}}))
	put(e.Store.PutItem(&Item{Kind: KindDoc, ID: "d2", Projects: []string{"drop"}}))
	at := time.Now().Add(-2 * time.Hour)
	put(e.Store.AppendLog("keep", at, "甲群", "甲的进展"))
	put(e.Store.AppendLog("drop", at.Add(time.Minute), "乙群", "乙的进展"))
	put(e.MergeProjects("keep", "drop", "去重 · 测试"))
}

func projectsOf(t *testing.T, e *Engine, kind, id string) string {
	t.Helper()
	var list []string
	switch kind {
	case KindChat:
		c, _ := e.Store.Chat(id)
		list = c.Projects
	case KindPerson:
		p, _ := e.Store.Person(id)
		list = p.Projects
	default:
		it, _ := e.Store.Item(kind, id)
		list = it.Projects
	}
	return strings.Join(slices.Sorted(slices.Values(list)), ",")
}

// A recorded merge is undone exactly: the kept project has its own lists back, what only the merged one had names it
// alone again, what both had names both, and dedupe never pairs them again.
func TestUnmergeUndoesARecordedMerge(t *testing.T) {
	m := &scriptModel{replies: []string{`{"same":true,"sure":true,"keep":"A","reason":"同一件事"}`}}
	e := testEngine(t, m)
	mergeFixture(t, e)
	if p := e.FindProject("示例 乙"); p == nil || p.Slug != "keep" {
		t.Fatalf("before unmerge 示例 乙 -> %+v", p)
	}

	id, err := e.Unmerge("示例 乙") // by name: the merged project, not the one its name now finds
	if err != nil || id != "drop" {
		t.Fatalf("unmerge = %q %v", id, err)
	}
	keep, _ := e.Store.Project("keep")
	drop, _ := e.Store.Project("drop")
	for name, got := range map[string][]string{"aliases": keep.Aliases, "people": keep.People, "chats": keep.Chats, "items": keep.Items, "dirs": keep.Dirs, "keywords": keep.Keywords} {
		want := map[string]string{"aliases": "甲计划", "people": "p1,p2", "chats": "c1", "items": "docs/d1", "dirs": "m:/a", "keywords": ""}[name]
		if strings.Join(got, ",") != want {
			t.Errorf("keep %s = %v, want %s", name, got, want)
		}
	}
	if len(keep.Moments) != 1 || keep.Moments[0].ID != "m1" || !keep.Dirty {
		t.Errorf("keep = %+v", keep)
	}
	if drop.Status != "paused" || drop.MergedInto != "" || drop.Merge != nil || !drop.Dirty || !drop.Live() {
		t.Errorf("drop = %+v", drop)
	}
	for ref, want := range map[string]string{"chats/c1": "drop,keep", "chats/c2": "drop", "people/p1": "keep", "people/p2": "drop,keep",
		"people/p3": "drop", "docs/d1": "keep", "docs/d2": "drop"} {
		kind, id, _ := strings.Cut(ref, "/")
		if got := projectsOf(t, e, kind, id); got != want {
			t.Errorf("%s names %s, want %s", ref, got, want)
		}
	}
	log := strings.Join(e.Store.Log("keep", 50), "\n")
	if strings.Contains(log, "乙的进展") || !strings.Contains(log, "甲的进展") || !strings.Contains(log, "拆开了项目「示例 乙」") {
		t.Errorf("keep's timeline:\n%s", log)
	}
	if log := strings.Join(e.Store.Log("drop", 50), "\n"); !strings.Contains(log, "乙的进展") {
		t.Errorf("drop's timeline:\n%s", log)
	}
	for name, want := range map[string]string{"示例 乙": "drop", "乙计划": "drop", "甲计划": "keep"} {
		if p := e.FindProject(name); p == nil || p.Slug != want {
			t.Errorf("%s -> %+v, want %s", name, p, want)
		}
	}
	// Not even once a verdict would be asked again (dedupeRecheck): the user's split stands.
	key := pairKey("keep", "drop")
	v := e.Store.dedupeVerdicts()[key]
	v.At = time.Now().Add(-2 * dedupeRecheck)
	if err := e.Store.putDedupeVerdict(key, v); err != nil {
		t.Fatal(err)
	}
	if n := e.DedupeProjects(context.Background(), 5); n != 0 || m.calls != 0 {
		t.Fatalf("the pair was asked about again: merged %d, %d call(s)", n, m.calls)
	}
}

// A merge from before the record: the kept project keeps the merged one's people, chats and items (it may have had
// them already) but not its name or aliases; what the merged one had names it again besides the kept one.
func TestUnmergeOlderMergeIsConservative(t *testing.T) {
	e := testEngine(t, nil)
	mergeFixture(t, e)
	drop, _ := e.Store.Project("drop")
	if drop.Merge == nil || strings.Join(drop.Merge.Moved, ",") != "chats/c2,people/p3,docs/d2" {
		t.Fatalf("merge record = %+v", drop.Merge)
	}
	drop.Merge = nil
	if err := e.Store.putEntity(KindProject, "drop", drop, &drop.Tagging, true); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Unmerge("drop"); err != nil {
		t.Fatal(err)
	}
	keep, _ := e.Store.Project("keep")
	drop, _ = e.Store.Project("drop")
	if strings.Join(keep.Aliases, ",") != "甲计划" || strings.Join(keep.People, ",") != "p1,p2,p3" || strings.Join(keep.Chats, ",") != "c1,c2" {
		t.Errorf("keep = %+v", keep)
	}
	if drop.Status != "active" || !drop.Live() {
		t.Errorf("drop = %+v", drop)
	}
	if got := projectsOf(t, e, KindChat, "c2"); got != "drop,keep" {
		t.Errorf("c2 names %s", got)
	}
	if log := strings.Join(e.Store.Log("keep", 50), "\n"); strings.Contains(log, "乙的进展") {
		t.Errorf("keep's timeline:\n%s", log)
	}
}

// A manual merge joined the pinned facts and the locks; unmerging gives the kept project its own back, unless they were
// edited since.
func TestUnmergeRestoresPinnedFacts(t *testing.T) {
	for _, edited := range []bool{false, true} {
		e := testEngine(t, nil)
		keepFact, dropFact := "甲已确认", "乙已确认"
		_ = e.Store.PutProject(&Project{Slug: "keep", Name: "示例 甲", Status: "active", Tagging: Tagging{Pinned: keepFact}})
		_ = e.Store.PutProject(&Project{Slug: "drop", Name: "示例 乙", Status: "active", Tagging: Tagging{Pinned: dropFact, Locked: true, LockedFields: []string{"goal"}}})
		if _, err := e.Apply(KindProject, "drop", Update{MergeInto: "keep"}); err != nil {
			t.Fatal(err)
		}
		if edited {
			later := "改过的事实"
			if _, err := e.Apply(KindProject, "keep", Update{Pinned: &later}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.Unmerge("drop"); err != nil {
			t.Fatal(err)
		}
		keep, _ := e.Store.Project("keep")
		drop, _ := e.Store.Project("drop")
		want := keepFact
		if edited {
			want = "改过的事实"
		}
		if keep.Pinned != want || drop.Pinned != dropFact {
			t.Errorf("edited=%v: keep pinned %q, drop pinned %q", edited, keep.Pinned, drop.Pinned)
		}
		if !edited && (keep.Locked || len(keep.LockedFields) != 0) {
			t.Errorf("keep kept drop's locks: locked %v, fields %v", keep.Locked, keep.LockedFields)
		}
	}
}

// Two projects merged into one, sharing a person, a chat and a timeline line the kept one did not have, unmerged in
// either order: unmerging one leaves them on the kept project, because the other merge still brought them.
func TestUnmergeKeepsWhatAnotherMergeBrought(t *testing.T) {
	for _, order := range [][]string{{"a", "c"}, {"c", "a"}} {
		e := testEngine(t, nil)
		at := time.Now().Add(-time.Hour)
		_ = e.Store.PutProject(&Project{Slug: "b", Name: "项目b", Status: "active", People: []string{"p0"}})
		for _, slug := range []string{"a", "c"} {
			_ = e.Store.PutProject(&Project{Slug: slug, Name: "项目" + slug, Status: "active", People: []string{"p"}, Chats: []string{"g"}})
			_ = e.Store.AppendLog(slug, at, "共享群", "同一条进展")
		}
		_ = e.Store.PutPerson(&Person{ID: "p", Projects: []string{"a", "c"}})
		_ = e.Store.PutChat(&Chat{ID: "g", Kind: "group", Projects: []string{"a", "c"}})
		for _, slug := range []string{"a", "c"} {
			if err := e.MergeProjects("b", slug, "测试"); err != nil {
				t.Fatal(err)
			}
		}

		names := func(stage, want string) {
			t.Helper()
			for _, ref := range [][2]string{{KindPerson, "p"}, {KindChat, "g"}} {
				if got := projectsOf(t, e, ref[0], ref[1]); got != want {
					t.Errorf("%s: %s/%s names %s, want %s", stage, ref[0], ref[1], got, want)
				}
			}
		}

		first := order[0]
		if _, err := e.Unmerge(first); err != nil {
			t.Fatal(err)
		}
		b, _ := e.Store.Project("b")
		if strings.Join(b.People, ",") != "p0,p" || strings.Join(b.Chats, ",") != "g" {
			t.Errorf("%s unmerged: b = %+v", first, b)
		}
		names(first+" unmerged", strings.Join(slices.Sorted(slices.Values([]string{first, "b"})), ","))
		if log := strings.Join(e.Store.Log("b", 50), "\n"); !strings.Contains(log, "同一条进展") {
			t.Errorf("%s unmerged: b's timeline:\n%s", first, log)
		}

		if _, err := e.Unmerge(order[1]); err != nil {
			t.Fatal(err)
		}
		b, _ = e.Store.Project("b")
		if strings.Join(b.People, ",") != "p0" || len(b.Chats) != 0 {
			t.Errorf("after both (%v): b = %+v", order, b)
		}
		names(fmt.Sprintf("after both (%v)", order), "a,c")
	}
}

// The kept project's own Moments posts that the merged project's newer ones pushed past the cap come back.
func TestUnmergeRestoresEvictedMoments(t *testing.T) {
	e := testEngine(t, nil)
	mine := posts("k", maxProjectMoments, time.Now().Add(-48*time.Hour))
	_ = e.Store.PutProject(&Project{Slug: "keep", Name: "示例 甲", Status: "active", Moments: mine})
	_ = e.Store.PutProject(&Project{Slug: "drop", Name: "示例 乙", Status: "active", Moments: posts("d", 10, time.Now())})
	if err := e.MergeProjects("keep", "drop", "测试"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Unmerge("drop"); err != nil {
		t.Fatal(err)
	}
	if keep, _ := e.Store.Project("keep"); !slices.Equal(momentIDs(keep.Moments), momentIDs(mine)) {
		t.Fatalf("keep's posts = %v", momentIDs(keep.Moments))
	}
}

// Only a merged project can be unmerged, and a chain comes apart from its last merge back.
func TestUnmergeRefuses(t *testing.T) {
	e := testEngine(t, nil)
	for _, slug := range []string{"a", "b", "c"} {
		_ = e.Store.PutProject(&Project{Slug: slug, Name: "项目" + slug, Status: "active"})
	}
	if _, err := e.Unmerge("a"); err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("live: %v", err)
	}
	if _, err := e.Unmerge("没有这个"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := e.MergeProjects("b", "a", "测试"); err != nil {
		t.Fatal(err)
	}
	if err := e.MergeProjects("c", "b", "测试"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Unmerge("a"); err == nil || !strings.Contains(err.Error(), "unmerge b first") {
		t.Fatalf("chain: %v", err)
	}
	if _, err := e.Unmerge("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Unmerge("a"); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"a", "b", "c"} {
		if p, _ := e.Store.Project(slug); !p.Live() {
			t.Errorf("%s = %+v", slug, p)
		}
	}
}

// posts are n Moments posts named prefix00…, the first at newest and each one a minute older.
func posts(prefix string, n int, newest time.Time) []ProjectMoment {
	var out []ProjectMoment
	for i := range n {
		out = append(out, ProjectMoment{ID: fmt.Sprintf("%s%02d", prefix, i), At: newest.Add(-time.Duration(i) * time.Minute)})
	}
	return out
}

// newest are the ids of the newest maxProjectMoments posts among lists.
func newest(lists ...[]ProjectMoment) []string {
	return momentIDs(mergeMoments(nil, slices.Concat(lists...)))
}

// Projects merged into one (in slug order) and unmerged one by one in different orders: after each unmerge the kept
// project has the newest posts of its own and of those still merged, when a later merge pushed an earlier one's posts
// past the cap, when two brought the same newer posts, when each pushed some of its own out, and when a post two
// brought was pushed out by a third.
func TestUnmergeSiblingsRestoreMoments(t *testing.T) {
	now := time.Now()
	shared := posts("s", 1, now.Add(-48*time.Hour).Add(-10*time.Minute-30*time.Second)) // between b10 and b11
	for _, tc := range []struct {
		name    string
		b       []ProjectMoment
		brought map[string][]ProjectMoment
		orders  [][]string
	}{
		{"pushed out a sibling's", posts("b", 45, now.Add(-time.Hour)),
			map[string][]ProjectMoment{"a": posts("a", 5, now.Add(-48*time.Hour)), "c": posts("c", 5, now)}, [][]string{{"a", "c"}, {"c", "a"}}},
		{"shared posts", posts("b", maxProjectMoments, now.Add(-48*time.Hour)),
			map[string][]ProjectMoment{"a": posts("s", 10, now), "c": posts("s", 10, now)}, [][]string{{"a", "c"}, {"c", "a"}}},
		{"both pushed", posts("b", maxProjectMoments, now.Add(-48*time.Hour)),
			map[string][]ProjectMoment{"a": posts("a", 5, now.Add(-time.Hour)), "c": posts("c", 5, now)}, [][]string{{"a", "c"}, {"c", "a"}}},
		{"shared post pushed by a third", posts("b", maxProjectMoments, now.Add(-48*time.Hour)),
			map[string][]ProjectMoment{"a": shared, "c": shared, "d": posts("d", 45, now)}, [][]string{{"a", "d", "c"}, {"d", "a", "c"}, {"c", "a", "d"}}},
	} {
		slugs := slices.Sorted(maps.Keys(tc.brought))
		for _, order := range tc.orders {
			e := testEngine(t, nil)
			_ = e.Store.PutProject(&Project{Slug: "b", Name: "项目b", Status: "active", Moments: tc.b})
			for _, slug := range slugs {
				_ = e.Store.PutProject(&Project{Slug: slug, Name: "项目" + slug, Status: "active", Moments: tc.brought[slug]})
			}
			for _, slug := range slugs {
				if err := e.MergeProjects("b", slug, "测试"); err != nil {
					t.Fatal(err)
				}
			}
			for i, slug := range order {
				if _, err := e.Unmerge(slug); err != nil {
					t.Fatal(err)
				}
				lists := [][]ProjectMoment{tc.b}
				for _, still := range order[i+1:] {
					lists = append(lists, tc.brought[still])
				}
				if b, _ := e.Store.Project("b"); !slices.Equal(momentIDs(b.Moments), newest(lists...)) {
					t.Errorf("%s, unmerged %v: b's posts = %v\nwant %v", tc.name, order[:i+1], momentIDs(b.Moments), newest(lists...))
				}
			}
		}
	}
}

// What the unmerged project's merge brought leaves the kept project when no later merge that still holds has it
// too: a merge from before had it already (here the user took it off in between), and a project made live again
// holds nothing.
func TestUnmergeHandsOverOnlyToLaterMerges(t *testing.T) {
	noLock := false
	for _, tc := range []string{"earlier", "revived"} {
		e := testEngine(t, nil)
		_ = e.Store.PutProject(&Project{Slug: "b", Name: "项目b", Status: "active"})
		for _, slug := range []string{"a", "c"} {
			_ = e.Store.PutProject(&Project{Slug: slug, Name: "项目" + slug, Status: "active", People: []string{"p"}})
		}
		_ = e.Store.PutPerson(&Person{ID: "p", Projects: []string{"a", "c"}})
		steps := []func() error{
			func() error { return e.MergeProjects("b", "c", "测试") },
			func() error {
				_, err := e.Apply(KindProject, "b", Update{RemovePeople: []string{"p"}, Lock: &noLock})
				return err
			},
			func() error { return e.MergeProjects("b", "a", "测试") },
		}
		if tc == "revived" {
			steps = []func() error{
				func() error { return e.MergeProjects("b", "a", "测试") },
				func() error { return e.MergeProjects("b", "c", "测试") },
				func() error {
					_, err := e.Apply(KindProject, "c", Update{Status: "active"})
					return err
				},
			}
		}
		for _, step := range steps {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.Unmerge("a"); err != nil {
			t.Fatal(err)
		}
		if b, _ := e.Store.Project("b"); slices.Contains(b.People, "p") {
			t.Errorf("%s: b's people = %v", tc, b.People)
		}
		if got := projectsOf(t, e, KindPerson, "p"); strings.Contains(got, "b") {
			t.Errorf("%s: p names %s", tc, got)
		}
	}
}

// A timeline line appended to the kept project after a manual merge leaves its locks as the merge set them, so
// unmerging still gives it its own back.
func TestUnmergeRestoresLocksAfterALogLine(t *testing.T) {
	e := testEngine(t, nil)
	_ = e.Store.PutProject(&Project{Slug: "keep", Name: "示例 甲", Status: "active", Tagging: Tagging{LockedFields: []string{"summary"}}})
	_ = e.Store.PutProject(&Project{Slug: "drop", Name: "示例 乙", Status: "active", Tagging: Tagging{LockedFields: []string{"goal"}}})
	if _, err := e.Apply(KindProject, "drop", Update{MergeInto: "keep"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(KindProject, "keep", Update{Log: "新进展"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Unmerge("drop"); err != nil {
		t.Fatal(err)
	}
	if keep, _ := e.Store.Project("keep"); !slices.Equal(keep.LockedFields, []string{"summary"}) {
		t.Fatalf("keep's locked fields = %v", keep.LockedFields)
	}
}
