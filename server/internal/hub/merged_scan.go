package hub

import (
	"bytes"
	"encoding/json"
	"slices"
)

// A merged list (writeMerged) is written from one scan of each answer, byte for byte what decoding it and encoding it
// again writes (writeDecoded). The answer is checked once with json.Valid, so the scan can trust its structure; it
// cuts the answer into its top level's members and the list's items without decoding them, and copies every value
// encoding/json would write unchanged: compact, with no '<', '>', '&', U+2028 or U+2029 in a string. Anything else is
// handed to encoding/json, never escaped by hand: its rules are not the classic ones (a RawMessage's U+2028 is
// escaped even with SetEscapeHTML(false), docs/lessons/server.md).

// answerScan is an answer cut as writeDecoded reads it.
type answerScan struct {
	members []jsonMember  // the top level's members but the list, sorted by key
	items   []scannedItem // the list's items
}

// jsonMember is one member of an object: its key's bytes between the quotes, its value's.
type jsonMember struct {
	key, val []byte
	asIs     bool // the value is written as it is
}

// scannedItem is one item of a list and how it is written with its manager.
type scannedItem struct {
	raw   []byte
	shape itemShape
}

type itemShape uint8

const (
	itemSlow  itemShape = iota // stampManager, then encoding/json
	itemAsIs                   // not an object: written as it is
	itemStamp                  // an object without "manager": written with it put first
)

// scanUse says what writeMerged does with an answer.
type scanUse uint8

const (
	scanMerge  scanUse = iota // merge its scan
	scanSkip                  // leave it out: not JSON, or not an object (null included)
	scanDecode                // only decoding reads it right: the whole merge goes to writeDecoded
)

// scanAnswer cuts body as writeDecoded reads it. It decodes (scanDecode) when a top-level key is escaped, outside
// printable ASCII, one of '<', '>', '&' or given twice (how a map keeps and sorts such keys is decoding's to say),
// and when the list is neither an array nor null.
func scanAnswer(body []byte, key string) (answerScan, scanUse) {
	var s answerScan
	if !json.Valid(body) {
		return s, scanSkip
	}
	i := jsonWS(body, 0)
	if body[i] != '{' {
		return s, scanSkip
	}
	members, _, plain := jsonMembers(body, i, make([]jsonMember, 0, 8))
	if !plain {
		return s, scanDecode
	}
	slices.SortFunc(members, func(x, y jsonMember) int { return bytes.Compare(x.key, y.key) })
	var list []byte
	for j, m := range members {
		if j > 0 && bytes.Equal(m.key, members[j-1].key) {
			return s, scanDecode
		}
		if string(m.key) == key {
			list = m.val
		}
	}
	if list != nil {
		members = slices.DeleteFunc(members, func(m jsonMember) bool { return string(m.key) == key })
		switch {
		case list[0] == '[':
			s.items = scanItems(list, nil)
		case string(list) != "null":
			return s, scanDecode
		}
	}
	s.members = members
	return s, scanMerge
}

// scanItems appends the items of the array list to out.
func scanItems(list []byte, out []scannedItem) []scannedItem {
	i := jsonWS(list, 1)
	for list[i] != ']' {
		end, shape := scanItem(list, i)
		if len(out) == 0 { // room for as many items like the first as the list holds, within reason
			out = slices.Grow(out, min(len(list)/max(end-i, 64), 4096))
		}
		out = append(out, scannedItem{raw: list[i:end], shape: shape})
		i = jsonWS(list, end)
		if list[i] == ',' {
			i = jsonWS(list, i+1)
		}
	}
	return out
}

// scanItem is the end of the item at b[i] and its shape: an object is stamped unless a key is escaped (it may read
// "manager") or is "manager", or anything in it is not written as it is.
func scanItem(b []byte, i int) (int, itemShape) {
	if b[i] != '{' {
		end, asIs := jsonValue(b, i)
		if asIs {
			return end, itemAsIs
		}
		return end, itemSlow
	}
	shape := itemStamp
	j := jsonWS(b, i+1)
	if j != i+1 {
		shape = itemSlow
	}
	if b[j] == '}' {
		return j + 1, shape
	}
	for {
		ke, escaped, encodes := jsonString(b, j)
		if escaped || encodes || string(b[j+1:ke-1]) == ManagerQuery {
			shape = itemSlow
		}
		if b[ke] != ':' {
			shape = itemSlow
		}
		v := jsonWS(b, jsonWS(b, ke)+1)
		if v != ke+1 {
			shape = itemSlow
		}
		ve, asIs := jsonValue(b, v)
		if !asIs {
			shape = itemSlow
		}
		j = jsonWS(b, ve)
		if j != ve {
			shape = itemSlow
		}
		if b[j] == '}' {
			return j + 1, shape
		}
		next := jsonWS(b, j+1) // after ','
		if next != j+1 {
			shape = itemSlow
		}
		j = next
	}
}

// scannedAnswer is an answer's scan with how the merge uses it.
type scannedAnswer struct {
	s   answerScan
	use scanUse
}

// scannedTodoIDs are the ids decodedTodoIDs reads from the body s is a scan of (for "todos"), and whether s alone says
// them: not when a key could be one of those encoding/json matches regardless of case, is in the list twice or is
// escaped, nor when an id is anything but plain ASCII.
func scannedTodoIDs(s answerScan, use scanUse) ([][]byte, bool) {
	switch use {
	case scanSkip: // no JSON object (null decodes to no todos, the rest do not decode)
		return nil, true
	case scanDecode:
		return nil, false
	}
	for _, m := range s.members { // the list's own key is out of them
		if bytes.EqualFold(m.key, []byte("todos")) {
			return nil, false
		}
	}
	ids := make([][]byte, 0, len(s.items))
	var members []jsonMember
	for _, it := range s.items {
		switch it.raw[0] {
		case 'n': // null: a todo without an id
			ids = append(ids, nil)
			continue
		case '{':
		default: // no todo: the body does not decode
			return nil, true
		}
		var id []byte
		var plain bool
		if members, _, plain = jsonMembers(it.raw, 0, members[:0]); !plain {
			return nil, false
		}
		seen := false
		for _, m := range members {
			if !bytes.EqualFold(m.key, []byte("id")) {
				continue
			}
			if seen || string(m.key) != "id" {
				return nil, false
			}
			seen = true
			switch m.val[0] {
			case '"':
				id = m.val[1 : len(m.val)-1]
				for _, c := range id {
					if c < 0x20 || c > 0x7e || c == '\\' {
						return nil, false
					}
				}
			case 'n': // null leaves the id unset
			default: // an id that is no string: the body does not decode
				return nil, true
			}
		}
		ids = append(ids, id)
	}
	return ids, true
}

// jsonMembers appends the members of the object at b[i] ('{') to out and returns where it ends. plain is false when a
// key is escaped, has a byte outside printable ASCII, or a '<', '>' or '&'.
func jsonMembers(b []byte, i int, out []jsonMember) (members []jsonMember, end int, plain bool) {
	plain = true
	i = jsonWS(b, i+1)
	if b[i] == '}' {
		return out, i + 1, plain
	}
	for {
		ke, escaped, encodes := jsonString(b, i)
		key := b[i+1 : ke-1]
		if escaped || encodes {
			plain = false
		}
		for _, c := range key {
			if c < 0x20 || c > 0x7e {
				plain = false
			}
		}
		v := jsonWS(b, jsonWS(b, ke)+1) // after ':'
		ve, asIs := jsonValue(b, v)
		out = append(out, jsonMember{key: key, val: b[v:ve], asIs: asIs})
		i = jsonWS(b, ve)
		if b[i] == '}' {
			return out, i + 1, plain
		}
		i = jsonWS(b, i+1) // after ','
	}
}

// jsonValue is the end of the value at b[i] and whether encoding/json writes it as it is: compact, and no string in it
// with something it escapes.
func jsonValue(b []byte, i int) (int, bool) {
	switch b[i] {
	case '"':
		end, _, encodes := jsonString(b, i)
		return end, !encodes
	case '{', '[':
		asIs := true
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				end, _, encodes := jsonString(b, i)
				if encodes {
					asIs = false
				}
				i = end
				continue
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1, asIs
				}
			case ' ', '\t', '\n', '\r':
				asIs = false
			}
			i++
		}
		return i, asIs
	default: // a number, true, false or null
		for i < len(b) {
			switch b[i] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				return i, true
			}
			i++
		}
		return i, true
	}
}

// jsonString: b[i] is '"'. It returns the index after the closing quote, whether the string has an escape, and whether
// encoding/json would change it: '<', '>', '&' (escaped for HTML), U+2028 or U+2029 (escaped always).
func jsonString(b []byte, i int) (end int, escaped, encodes bool) {
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			escaped = true
			i++
		case '"':
			return i + 1, escaped, encodes
		case '<', '>', '&':
			encodes = true
		case 0xe2: // U+2028 and U+2029 are E2 80 A8 and E2 80 A9
			if i+2 < len(b) && b[i+1] == 0x80 && b[i+2]&^1 == 0xa8 {
				encodes = true
			}
		}
	}
	return i, escaped, encodes
}

func jsonWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}
