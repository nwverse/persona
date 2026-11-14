package transcript

import (
	containerheap "container/heap"
	"fmt"
	"sort"
)

// inputClipMarker is deliberately after ordinary argument names in sorted-key subject fallbacks. Existing arguments stay
// ahead of it in the usual case; an input with no earlier string can use the omission note instead of a bare tool name.
const inputClipMarker = "zz_persona_clipped"

// inputClipNode indexes one value in the JSON produced by the string passes. size is its current encoded size, including
// brackets or braces for containers. Object children retain the candidate's key order; the root marker is appended last
// and is the one fixed child. parent lets a removal update every ancestor's size without encoding any subtree again.
type inputClipNode struct {
	kind    byte // '[', '{', or 0 for a scalar
	value   any  // scalar value; containers rebuild their value from children
	key     string
	keySize int // encoded key size when the parent is an object
	parent  *inputClipNode
	kids    []*inputClipNode
	fixed   int
	size    int
	order   int
	active  bool
	index   int // position in inputClipHeap
}

type inputClipTree struct {
	arrays  []*inputClipNode
	objects []*inputClipNode
	order   int
}

func newInputClipTree(v any) *inputClipTree {
	t := &inputClipTree{}
	t.build(v, nil)
	return t
}

func (t *inputClipTree) build(v any, parent *inputClipNode) *inputClipNode {
	n := &inputClipNode{value: v, parent: parent, active: true, index: -1}
	switch v := v.(type) {
	case []any:
		n.kind = '['
		n.order = t.order
		t.order++
		t.arrays = append(t.arrays, n)
		n.size = 2
		for i, e := range v {
			child := t.build(e, n)
			if i > 0 {
				n.size++
			}
			n.size += child.size
			n.kids = append(n.kids, child)
		}
	case map[string]any:
		n.kind = '{'
		n.order = t.order
		t.order++
		t.objects = append(t.objects, n)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys) // the same order as encoding/json, which produced the candidate
		n.size = 2
		for i, k := range keys {
			child := t.build(v[k], n)
			child.key, child.keySize = k, jsonLen(k)
			if i > 0 {
				n.size++
			}
			n.size += child.keySize + 1 + child.size
			n.kids = append(n.kids, child)
		}
	default:
		n.size = len(marshalPlain(v))
	}
	return n
}

// inputClipHeap chooses the longest array or widest object, with the first container in document order breaking ties.
type inputClipHeap []*inputClipNode

func (h inputClipHeap) Len() int { return len(h) }
func (h inputClipHeap) Less(i, j int) bool {
	a, b := h[i].remaining(), h[j].remaining()
	if a != b {
		return a > b
	}
	return h[i].order < h[j].order
}
func (h inputClipHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *inputClipHeap) Push(v any) {
	n := v.(*inputClipNode)
	n.index = len(*h)
	*h = append(*h, n)
}
func (h *inputClipHeap) Pop() any {
	old := *h
	n := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	n.index = -1
	return n
}

func (n *inputClipNode) remaining() int { return len(n.kids) - n.fixed }

// nextInputClipNode lazily discards containers removed with an ancestor. A zero at the top means no attached container
// has anything left to drop: remaining count, not activity, determines heap order, so inactive positive counts pop first.
func nextInputClipNode(h *inputClipHeap) *inputClipNode {
	for h.Len() > 0 {
		n := (*h)[0]
		if !n.active {
			containerheap.Pop(h)
			continue
		}
		if n.remaining() == 0 {
			return nil
		}
		return n
	}
	return nil
}

func deactivateInputClipNode(n *inputClipNode) {
	n.active = false
	for _, child := range n.kids {
		deactivateInputClipNode(child)
	}
}

func (n *inputClipNode) resize(delta int) {
	for p := n; p != nil; p = p.parent {
		p.size += delta
	}
}

func dropInputClipTail(h *inputClipHeap, n *inputClipNode) {
	last := len(n.kids) - 1 - n.fixed // fixed suffix children (the root marker) are never removed
	child := n.kids[last]
	saved := child.size
	if n.kind == '{' {
		saved += child.keySize + 1
	}
	if len(n.kids) > 1 {
		saved++ // the comma before the removed member
	}
	n.kids = append(n.kids[:last], n.kids[last+1:]...)
	n.resize(-saved)
	deactivateInputClipNode(child)
	containerheap.Fix(h, n.index)
}

func (n *inputClipNode) decodedValue() any {
	switch n.kind {
	case '[':
		out := make([]any, len(n.kids))
		for i, child := range n.kids {
			out[i] = child.decodedValue()
		}
		return out
	case '{':
		out := make(map[string]any, len(n.kids))
		for _, child := range n.kids {
			out[child.key] = child.decodedValue()
		}
		return out
	default:
		return n.value
	}
}

func inputClipNote(originalSize, arrayItems, objectFields int) string {
	return fmt.Sprintf("Omitted %d array items and %d object fields."+clipNoteFormat, arrayItems, objectFields, originalSize)
}

// boundInputJSON applies the third and final clip pass to an over-budget candidate from the string passes. It removes
// array tails, longest by current item count first, then object trailing fields, widest by current field count first.
// Every surviving value keeps its type and place. The root marker names the direct removals and ends with the existing
// cut note; its encoded bytes are included throughout, so the returned compact JSON is at most maxInputLen bytes.
func boundInputJSON(candidate []byte, originalSize int) []byte {
	return boundInputJSONBudget(candidate, originalSize, maxInputLen)
}

func boundInputJSONBudget(candidate []byte, originalSize, budget int) []byte {
	value, err := decodeInputObject(candidate)
	if err != nil {
		return candidate // candidate came from marshalPlain; keep the old behavior if that ever changes
	}
	tree := newInputClipTree(value)
	rootNode := tree.objects[0] // the document root is the first object in preorder
	arrays := inputClipHeap(tree.arrays)
	objects := inputClipHeap(tree.objects)
	for i, n := range arrays {
		n.index = i
	}
	for i, n := range objects {
		n.index = i
	}
	containerheap.Init(&arrays)
	containerheap.Init(&objects)

	markerKey := inputClipMarker
	for suffix := 2; ; suffix++ {
		if _, exists := value[markerKey]; !exists {
			break
		}
		markerKey = fmt.Sprintf("%s_%d", inputClipMarker, suffix)
	}
	marker := &inputClipNode{value: inputClipNote(originalSize, 0, 0), key: markerKey, keySize: jsonLen(markerKey), parent: rootNode, active: true, index: -1}
	marker.size = jsonLen(marker.value.(string))
	added := marker.keySize + 1 + marker.size
	if len(rootNode.kids) > 0 {
		added++
	}
	rootNode.kids = append(rootNode.kids, marker)
	rootNode.fixed = 1
	rootNode.resize(added)
	containerheap.Fix(&objects, rootNode.index)

	arrayItems, objectFields := 0, 0
	for rootNode.size > budget {
		if n := nextInputClipNode(&arrays); n != nil {
			dropInputClipTail(&arrays, n)
			arrayItems++
		} else if n := nextInputClipNode(&objects); n != nil {
			dropInputClipTail(&objects, n)
			objectFields++
		} else {
			break // only the small root marker remains, which always fits
		}
		note := inputClipNote(originalSize, arrayItems, objectFields)
		delta := jsonLen(note) - marker.size
		marker.value = note
		marker.size += delta
		marker.parent.resize(delta)
	}
	return marshalPlain(rootNode.decodedValue())
}
