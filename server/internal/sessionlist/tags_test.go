package sessionlist

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func itemWithTags(item Item, tags ...string) Item {
	item.Fold = &Fold{State: item.State, BackgroundWaiting: item.BackgroundWaiting, Tags: tags}
	return item
}

func TestCanonicalTagsAndParse(t *testing.T) {
	input := []string{" Work ", "alpha", "WORK", "Alpha", "", "  ", "beta"}
	want := []string{"alpha", "beta", "Work"}
	if got := CanonicalTags(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalTags = %q, want %q", got, want)
	}
	if input[0] != " Work " {
		t.Fatal("CanonicalTags modified its input")
	}
	if got := CanonicalTags([]string{"", "  "}); got != nil {
		t.Fatalf("empty canonical tags = %#v, want nil", got)
	}

	values := url.Values{"tag": {" Work ", "alpha", "WORK", "Alpha"}}
	o, err := Parse(values)
	if err != nil || !Enabled(values) || !reflect.DeepEqual(o.Tags, []string{"alpha", "Work"}) {
		t.Fatalf("parsed tags = %+v, %v", o, err)
	}
	sixteen := make([]string, maxTags)
	for n := range sixteen {
		sixteen[n] = fmt.Sprint("tag-", n)
	}
	if _, err := Parse(url.Values{"tag": sixteen}); err != nil {
		t.Fatalf("16 selections rejected: %v", err)
	}
	tooLong := strings.Repeat("界", maxTagCharacters+1)
	invalid := []url.Values{
		{"tag": nil},
		{"tag": {""}},
		{"tag": {"   "}},
		{"tag": {"work\nhome"}},
		{"tag": {"\twork"}},
		{"tag": {"work\x00home"}},
		{"tag": {string([]byte{0xff})}},
		{"tag": {tooLong}},
		{"tag": append(sixteen, "one-too-many")},
		{"tag": make([]string, maxTags+1)}, // the raw selection count is bounded even before deduplication
	}
	for _, values := range invalid {
		if _, err := Parse(values); err == nil {
			t.Errorf("accepted invalid tags %q", values["tag"])
		} else {
			requireCode(t, err, "invalid_query")
		}
	}
	if _, err := Parse(url.Values{"tag": {strings.Repeat("界", maxTagCharacters)}}); err != nil {
		t.Fatalf("Unicode character limit counted bytes: %v", err)
	}
	var index Index
	for _, tags := range [][]string{{""}, {"work\r"}, {tooLong}, append(sixteen, "extra")} {
		_, err := index.Query(Options{Tags: tags})
		requireCode(t, err, "invalid_query")
		_, err = index.Counts(Options{Tags: tags})
		requireCode(t, err, "invalid_query")
	}
}

func TestTagCursorAndCacheBinding(t *testing.T) {
	var index Index
	index.Replace("demo", []Item{
		itemWithTags(testItem("a", 1), "Work"),
		itemWithTags(testItem("b", 2), "Alpha"),
		itemWithTags(testItem("c", 3), "Other"),
	})
	firstOptions, err := Parse(url.Values{"limit": {"1"}, "tag": {" Work ", "ALPHA", "work"}})
	if err != nil {
		t.Fatal(err)
	}
	first := mustQuery(t, &index, firstOptions)
	if first.Total != 2 || !first.HasMore || !reflect.DeepEqual(sessionIDs(first), []string{"b"}) {
		t.Fatalf("first tagged page = %+v", first)
	}
	validated, _ := validate(firstOptions)
	cached := index.cache[cacheKey{filters: validated.filters()}]
	if cached == nil {
		t.Fatal("tagged view was not cached")
	}
	equivalent := mustQuery(t, &index, Options{Tags: []string{"alpha", "WORK"}})
	if equivalent.Total != 2 || index.cache[cacheKey{filters: validated.filters()}].result != cached.result {
		t.Fatal("case and order changed the tag cache key")
	}
	continuation, err := Parse(url.Values{"cursor": {first.NextCursor}, "tag": {"alpha", "WORK"}})
	if err != nil {
		t.Fatal(err)
	}
	last := mustQuery(t, &index, continuation)
	if last.HasMore || !reflect.DeepEqual(sessionIDs(last), []string{"a"}) || last.Total != 2 || last.Revision != first.Revision {
		t.Fatalf("canonical continuation = %+v", last)
	}
	for _, tags := range [][]string{nil, {"Work"}, {"Alpha", "Work", "Other"}} {
		_, err := index.Query(Options{Cursor: first.NextCursor, Tags: tags})
		requireCode(t, err, "invalid_query")
	}
}

func TestTagORAndOtherFiltersAndCounts(t *testing.T) {
	var index Index
	a := itemWithTags(testItem("a", 1), " Work ", "home", "WORK")
	b := itemWithTags(testItem("b", 2), "Work")
	b.State = "idle"
	b.Fold.State = "idle"
	c := itemWithTags(testItem("c", 3), "Personal")
	d := itemWithTags(testItem("d", 4), "Work", "Personal")
	d.Machine = "machine-b"
	e := testItem("e", 5) // no Fold: absence of tags must not exclude the row from an unfiltered view
	index.Replace("demo", []Item{a, b, c, d, e})

	if got := sessionIDs(mustQuery(t, &index, Options{})); !reflect.DeepEqual(got, []string{"e", "d", "c", "b", "a"}) {
		t.Fatalf("unfiltered IDs = %v", got)
	}
	if got := sessionIDs(mustQuery(t, &index, Options{Tags: []string{"missing", "work", "PERSONAL"}})); !reflect.DeepEqual(got, []string{"d", "c", "b", "a"}) {
		t.Fatalf("OR IDs = %v", got)
	}
	and := Options{Tags: []string{"work", "personal"}, State: "working", Machine: "machine-a"}
	if got := sessionIDs(mustQuery(t, &index, and)); !reflect.DeepEqual(got, []string{"c", "a"}) {
		t.Fatalf("OR plus AND IDs = %v", got)
	}
	if got := sessionIDs(mustQuery(t, &index, Options{Tags: []string{"home"}, State: "idle"})); len(got) != 0 {
		t.Fatalf("tag overrode state filter: %v", got)
	}

	stored := index.Items([]string{"a"})[0]
	if !reflect.DeepEqual(stored.Fold.Tags, []string{"home", "Work"}) {
		t.Fatalf("stored canonical tags = %q", stored.Fold.Tags)
	}
	counts, err := index.Counts(Options{State: "working"}) // the facet omits tag filters but keeps the state chip
	if err != nil || counts.Total != 4 || counts.Tags["Work"] != 2 || counts.Tags["Personal"] != 2 || counts.Tags["home"] != 1 || len(counts.Tags) != 3 {
		t.Fatalf("tag facet = %+v, %v", counts, err)
	}
	filtered, err := index.Counts(Options{Tags: []string{"WORK"}})
	if err != nil || filtered.Total != 3 || filtered.Tags["Work"] != 3 || filtered.Tags["Personal"] != 1 || filtered.Tags["home"] != 1 {
		t.Fatalf("filtered tag counts = %+v, %v", filtered, err)
	}
	counts.Tags["Work"] = 99
	again, _ := index.Counts(Options{State: "working"})
	if again.Tags["Work"] != 2 {
		t.Fatal("caller mutated cached tag counts")
	}
}

func TestTagRootsAndFoldEquality(t *testing.T) {
	root := itemWithTags(testItem("root", 1), "RootTag")
	root.Fold.State = "needs_you"
	child := itemWithTags(testItem("child", 2), "ChildTag")
	child.Fold.Root = "root"
	var index Index
	index.Replace("demo", []Item{root, child})
	if page := mustQuery(t, &index, Options{Roots: true, Tags: []string{"roottag"}}); page.Total != 1 {
		t.Fatalf("root tag = %+v", page)
	}
	if page := mustQuery(t, &index, Options{Roots: true, Tags: []string{"ChildTag"}}); page.Total != 0 {
		t.Fatalf("folded child leaked into roots = %+v", page)
	}
	if page := mustQuery(t, &index, Options{Machine: "machine-a", Under: "root", Tags: []string{"CHILDTAG"}}); page.Total != 1 {
		t.Fatalf("child tag under root = %+v", page)
	}
	counts, err := index.Counts(Options{Roots: true})
	if err != nil || counts.Total != 1 || counts.Tags["RootTag"] != 1 || len(counts.Tags) != 1 {
		t.Fatalf("root tag counts = %+v, %v", counts, err)
	}

	a := itemWithTags(testItem("a", 1), "One", "Two")
	a.Fold.Root, a.Fold.Todo, a.Fold.BackgroundWaiting = "root", "todo_a", true
	b := a
	b.Fold = &Fold{Root: "root", State: a.State, Todo: "todo_a", BackgroundWaiting: true, Tags: []string{"One", "Two"}}
	if !sameItem(&a, &b) || !sameFold(a.Fold, b.Fold) {
		t.Fatal("equivalent independently allocated folds differ")
	}
	b.Fold.Tags[1] = "Changed"
	if sameItem(&a, &b) || sameFold(a.Fold, b.Fold) {
		t.Fatal("tag-only fold edit was treated as unchanged")
	}
	a.Fold.Tags = []string{}
	b.Fold = &Fold{Root: "root", State: a.State, Todo: "todo_a", BackgroundWaiting: true}
	if got, want := sameItem(&a, &b), reflect.DeepEqual(a, b); got != want {
		t.Fatalf("sameItem = %v, DeepEqual = %v for nil and empty tags", got, want)
	}

	baseline := index.Revision()
	root.Fold.Tags = []string{"RootTag", " ROOTTAG ", "RootTag"}
	index.Replace("demo", []Item{root, child})
	if index.Revision() != baseline {
		t.Fatal("canonically unchanged tags advanced the revision")
	}
}

func TestTagOnlyDeltaMembership(t *testing.T) {
	row := itemWithTags(testItem("row", 1), "Work")
	var index Index
	index.Replace("demo", []Item{row})
	baseline := index.Revision()
	row.Fold = &Fold{State: row.State, Tags: []string{" Personal "}}
	index.Replace("demo", []Item{row})
	if index.Revision() == baseline {
		t.Fatal("tag-only edit did not advance the revision")
	}
	if page := mustQuery(t, &index, Options{Since: baseline}); !reflect.DeepEqual(sessionIDs(page), []string{"row"}) || len(page.Remove) != 0 {
		t.Fatalf("unfiltered tag-only upsert = %+v", page)
	}
	if page := mustQuery(t, &index, Options{Since: baseline, Tags: []string{"Work"}}); len(page.Sessions) != 0 || !reflect.DeepEqual(page.Remove, []string{"row"}) || page.Total != 0 {
		t.Fatalf("tag exit = %+v", page)
	}
	if page := mustQuery(t, &index, Options{Since: baseline, Tags: []string{"personal"}}); !reflect.DeepEqual(sessionIDs(page), []string{"row"}) || len(page.Remove) != 0 || page.Total != 1 {
		t.Fatalf("tag entry = %+v", page)
	}
	if page := mustQuery(t, &index, Options{Since: baseline, Tags: []string{"Work", "Personal"}}); !reflect.DeepEqual(sessionIDs(page), []string{"row"}) || len(page.Remove) != 0 {
		t.Fatalf("tag edit within OR membership = %+v", page)
	}

	second := index.Revision()
	row.Fold.Tags = []string{"personal", "WORK"}
	index.Replace("demo", []Item{row})
	if page := mustQuery(t, &index, Options{Since: second, Tags: []string{"Personal"}}); !reflect.DeepEqual(sessionIDs(page), []string{"row"}) || len(page.Remove) != 0 {
		t.Fatalf("added tag while remaining selected = %+v", page)
	}
	if page := mustQuery(t, &index, Options{Since: second, Tags: []string{"Work"}}); !reflect.DeepEqual(sessionIDs(page), []string{"row"}) || len(page.Remove) != 0 {
		t.Fatalf("second tag entry = %+v", page)
	}
	counts, err := index.Counts(Options{})
	if err != nil || counts.Tags["personal"] != 1 || counts.Tags["WORK"] != 1 || len(counts.Tags) != 2 {
		t.Fatalf("counts after tag-only edits = %+v, %v", counts, err)
	}
}
