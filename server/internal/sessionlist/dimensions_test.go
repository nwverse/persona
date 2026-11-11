package sessionlist

import (
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"
)

func dimensionOpts(t *testing.T, query string) Options {
	t.Helper()
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseRuntime(q)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestRuntimeDimensionRepeatedDeviceOnly(t *testing.T) {
	q, _ := url.ParseQuery("machine_id=b&machine_id=a&limit=1")
	if !RuntimeEnabled(q) {
		t.Fatal("repeated device must opt in")
	}
	o, err := ParseRuntime(q)
	if err != nil {
		t.Fatal(err)
	}
	var idx Index
	for _, m := range []string{"a", "b", "ab"} {
		idx.Replace(m, []Item{{ID: m, Machine: m, Full: m}})
	}
	p, err := idx.Query(o)
	if err != nil || p.Total != 2 || !p.HasMore {
		t.Fatalf("page: %+v %v", p, err)
	}
	o = dimensionOpts(t, "machine_id=a&machine_id=b&machine_id=a&limit=1")
	o.Cursor = p.NextCursor
	p, err = idx.Query(o)
	if err != nil || !reflect.DeepEqual(p.Sessions, []any{"b"}) || p.HasMore {
		t.Fatalf("continuation: %+v %v", p, err)
	}
}

func TestRuntimeDimensionGroupsUnknownLastAndFrozen(t *testing.T) {
	item := func(id, p, h, title string, updated, created int64) Item {
		i := Item{ID: id, Machine: "device", Title: title, Dimensions: Dimensions{ProjectID: p, ProjectName: p, HarnessID: h, HarnessName: h}, Full: id}
		if updated != 0 {
			i.UpdatedAt = time.Unix(updated, 0)
		}
		if created != 0 {
			i.CreatedAt = time.Unix(created, 0)
		}
		return i
	}
	rows := []Item{
		item("z", "git:b", "codex", "Z", 20, 1),
		item("a", "git:b", "codex", "A", 10, 3),
		item("h", "git:b", "unknown", "H", 30, 0),
		item("b", "git:a", "codex", "B", 25, 2),
		item("t", "git:c", "codex", "T", 0, 0),
		item("u", "unknown:m", "codex", "U", 99, 0),
	}
	var idx Index
	idx.Replace("device", rows)
	for _, tc := range []struct {
		sort, dir string
		want      []any
	}{
		{"title", "asc", []any{"a", "z", "h", "b", "t", "u"}},
		{"title", "desc", []any{"z", "a", "h", "b", "t", "u"}},
		{"created", "asc", []any{"z", "a", "h", "b", "t", "u"}},
		{"created", "desc", []any{"a", "z", "h", "b", "t", "u"}},
	} {
		o := dimensionOpts(t, "facets=1&group_by=project,harness&limit=2&sort="+tc.sort+"&direction="+tc.dir)
		first, err := idx.Query(o)
		if err != nil || first.Total != len(rows) || !first.HasMore {
			t.Fatalf("first: %+v %v", first, err)
		}
		if g := *first.Groups; len(g) != 9 || !slices.Equal(g[0].Path, []string{"git:b"}) || g[0].Count != 3 ||
			!slices.Equal(g[len(g)-1].Path, []string{"unknown:m", "codex"}) {
			t.Fatalf("groups: %+v", g)
		}
		// A newer update would move git:a to the front; the old cursor must
		// keep its original order, membership and counts for the other pages.
		mutated := slices.Clone(rows)
		mutated[3].UpdatedAt = time.Unix(200, 0)
		idx.Replace("device", mutated[:4])
		got := slices.Clone(first.Sessions)
		for cursor := first.NextCursor; cursor != ""; {
			o.Cursor = cursor
			p, err := idx.Query(o)
			if err != nil || p.Revision != first.Revision || !reflect.DeepEqual(p.Facets, first.Facets) ||
				!reflect.DeepEqual(p.Groups, first.Groups) {
				t.Fatalf("continuation: %+v %v", p, err)
			}
			got = append(got, p.Sessions...)
			cursor = p.NextCursor
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s/%s: got %v want %v", tc.sort, tc.dir, got, tc.want)
		}
		idx.Replace("device", rows)
	}
}
