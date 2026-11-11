package sessionlist

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// Dimensions is the runtime adapter's identity and label projection. The
// device identity is Item.Machine; its display name may change independently.
type Dimensions struct {
	DeviceName, ProjectID, ProjectName, HarnessID, HarnessName string
}

type Facet struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Facets struct {
	Device  []Facet `json:"device"`
	Project []Facet `json:"project"`
	Harness []Facet `json:"harness"`
}

type GroupCount struct {
	Path  []string `json:"path"`
	Count int      `json:"count"`
}

type dimension int

const (
	device dimension = iota
	project
	harness
	dimensionCount
)

func groupDimensions(groupBy string) ([]dimension, error) {
	if groupBy == "" {
		return nil, nil
	}
	var seen [dimensionCount]bool
	var order []dimension
	for key := range strings.SplitSeq(groupBy, ",") {
		var d dimension
		switch key {
		case "device":
			d = device
		case "project":
			d = project
		case "harness":
			d = harness
		default:
			return nil, failure("invalid_query", "group_by contains an unknown dimension")
		}
		if seen[d] {
			return nil, failure("invalid_query", "group_by must not repeat a dimension")
		}
		seen[d] = true
		order = append(order, d)
	}
	return order, nil
}

func (v Item) dimension(d dimension) (id, name string) {
	switch d {
	case device:
		return v.Machine, v.Dimensions.DeviceName
	case project:
		return v.Dimensions.ProjectID, v.Dimensions.ProjectName
	default:
		return v.Dimensions.HarnessID, v.Dimensions.HarnessName
	}
}

type viewOptions struct {
	Facets                   bool
	GroupBy, Sort, Direction string
}

func (o Options) view() viewOptions {
	return viewOptions{Facets: o.Facets, GroupBy: o.GroupBy, Sort: o.Sort, Direction: o.Direction}
}

type groupKey struct {
	ids   [dimensionCount]string
	depth int
}

type groupStat struct {
	count  int
	latest time.Time
}

type facetStat struct {
	Facet
	folded string
}

type dimensionView struct {
	order  []dimension
	facets [dimensionCount]map[string]facetStat
	groups map[groupKey]groupStat
}

func newDimensionView(o viewOptions) *dimensionView {
	order, _ := groupDimensions(o.GroupBy) // options have already been validated
	v := &dimensionView{order: order}
	for d := range v.facets {
		v.facets[d] = make(map[string]facetStat)
	}
	if len(order) > 0 {
		v.groups = make(map[groupKey]groupStat)
	}
	return v
}

func (v *dimensionView) add(item Item) {
	for d := range v.facets {
		id, name := item.dimension(dimension(d))
		if id == "" {
			continue
		}
		name = cmp.Or(name, id)
		folded := normalizeQ(name)
		f := v.facets[d][id]
		if f.Count == 0 || folded < f.folded || folded == f.folded && name < f.Name {
			f.ID, f.Name, f.folded = id, name, folded
		}
		f.Count++
		v.facets[d][id] = f
	}
	var key groupKey
	for level, d := range v.order {
		key.ids[level], _ = item.dimension(d)
		key.depth++
		g := v.groups[key]
		g.count++
		if !item.UpdatedAt.IsZero() && (g.latest.IsZero() || item.UpdatedAt.After(g.latest)) {
			g.latest = item.UpdatedAt
		}
		v.groups[key] = g
	}
}

func (v *dimensionView) comparePath(a, b groupKey) int {
	var pa, pb groupKey
	for level := 0; level < min(a.depth, b.depth); level++ {
		pa.ids[level], pb.ids[level] = a.ids[level], b.ids[level]
		pa.depth, pb.depth = level+1, level+1
		if pa != pb {
			d := v.order[level]
			ua, ub := unknownGroup(d, pa.ids[level]), unknownGroup(d, pb.ids[level])
			if ua != ub {
				if ua {
					return 1
				}
				return -1
			}
			if c := compareTime(v.groups[pa].latest, v.groups[pb].latest, false); c != 0 {
				return c
			}
			fa, fb := v.facets[d][pa.ids[level]], v.facets[d][pb.ids[level]]
			if c := cmp.Compare(fa.folded, fb.folded); c != 0 {
				return c
			}
			return cmp.Compare(pa.ids[level], pb.ids[level])
		}
	}
	return cmp.Compare(a.depth, b.depth)
}

func unknownGroup(d dimension, id string) bool {
	return id == "" || (d == project && strings.HasPrefix(id, "unknown:")) || (d == harness && id == "unknown")
}

func (v *dimensionView) path(item Item) groupKey {
	key := groupKey{depth: len(v.order)}
	for level, d := range v.order {
		key.ids[level], _ = item.dimension(d)
	}
	return key
}

func (v *dimensionView) response(o viewOptions) (*Facets, *[]GroupCount) {
	if v == nil {
		return nil, nil
	}
	var facets *Facets
	if o.Facets {
		var all [dimensionCount][]Facet
		for d, options := range v.facets {
			all[d] = make([]Facet, 0, len(options))
			for _, f := range options {
				all[d] = append(all[d], f.Facet)
			}
			slices.SortFunc(all[d], func(a, b Facet) int {
				if c := cmp.Compare(options[a.ID].folded, options[b.ID].folded); c != 0 {
					return c
				}
				return cmp.Compare(a.ID, b.ID)
			})
		}
		facets = &Facets{Device: all[device], Project: all[project], Harness: all[harness]}
	}
	var groups *[]GroupCount
	if o.GroupBy != "" {
		paths := make([]groupKey, 0, len(v.groups))
		for key := range v.groups {
			paths = append(paths, key)
		}
		slices.SortFunc(paths, v.comparePath)
		out := make([]GroupCount, 0, len(paths))
		for _, key := range paths {
			out = append(out, GroupCount{Path: slices.Clone(key.ids[:key.depth]), Count: v.groups[key].count})
		}
		groups = &out
	}
	return facets, groups
}

// Zero time is always last, regardless of direction.
func compareTime(a, b time.Time, ascending bool) int {
	switch {
	case a.IsZero():
		if b.IsZero() {
			return 0
		}
		return 1
	case b.IsZero():
		return -1
	case ascending:
		return a.Compare(b)
	default:
		return b.Compare(a)
	}
}

func sortView(events []event, o viewOptions, dimensions *dimensionView) {
	if o.Sort == "" && o.GroupBy == "" {
		sortEvents(events)
		return
	}
	slices.SortFunc(events, func(a, b event) int {
		if dimensions != nil && len(dimensions.order) > 0 {
			if c := dimensions.comparePath(dimensions.path(a.value.item), dimensions.path(b.value.item)); c != 0 {
				return c
			}
		}
		var c int
		switch o.Sort {
		case "title":
			c = cmp.Compare(a.value.title, b.value.title)
			if o.Direction == "desc" {
				c = -c
			}
		case "created":
			c = compareTime(a.value.item.CreatedAt, b.value.item.CreatedAt, o.Direction == "asc")
		default:
			c = compareTime(a.updated, b.updated, o.Direction == "asc")
		}
		if c == 0 {
			return cmp.Compare(a.id, b.id)
		}
		return c
	})
}
