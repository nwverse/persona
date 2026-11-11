package proto

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// sessionSizeClass is the Go allocator's size class a Session allocated on its own lands in (the classes around it are
// 1,408, 1,536 and 1,792 bytes, runtime/sizeclasses.go). An object this size with pointers carries an 8-byte
// allocation header in its slot (Go 1.22's malloc headers), so a Session gets mallocHeader bytes less than the class.
const (
	sessionSizeClass = 1536
	mallocHeader     = 8
)

// A row is copied one at a time all over the hub, the manager and the node, and the allocator rounds each copy up to
// its size class. #3897 added two fields and took Session from 1,528 to 1,568 bytes: every row copied on its own then
// cost 1,792 bytes, and four allocation budgets in the assistant failed at once, none of them near the change (#3937).
// A field that does not fit makes room first: a bool between two wider fields takes an 8-byte word to itself, so move
// it beside other bools (the message lists the ones alone). If no room is left, measure what the bigger rows cost the
// allocation budgets and say why it is needed where those budgets are.
func TestSessionFitsItsSizeClass(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the size class is the 64-bit one")
	}
	if got := unsafe.Sizeof(Session{}); got > sessionSizeClass-mallocHeader {
		t.Errorf("Session is %d bytes, past the %d-byte size class with its %d-byte allocation header: a row copied on its "+
			"own now costs the next class (1,792 bytes). Bools that take a word to themselves: %s", got, sessionSizeClass,
			mallocHeader, strings.Join(loneBools(), ", "))
	}
}

// loneBools are Session's bool fields with no other bool in their 8-byte word.
func loneBools() []string {
	ty := reflect.TypeFor[Session]()
	words := map[uintptr][]string{}
	for i := range ty.NumField() {
		if f := ty.Field(i); f.Type.Kind() == reflect.Bool {
			words[f.Offset/8] = append(words[f.Offset/8], f.Name)
		}
	}
	var lone []string
	for i := range ty.NumField() {
		if f := ty.Field(i); f.Type.Kind() == reflect.Bool && len(words[f.Offset/8]) == 1 {
			lone = append(lone, f.Name)
		}
	}
	if lone == nil {
		return []string{"none"}
	}
	return lone
}
