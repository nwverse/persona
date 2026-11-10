package tzguard

import (
	"strings"
	"testing"
)

func TestMaskBooleanLocalCalls(t *testing.T) {
	cases := []struct {
		name, source string
		siblings     []string
		matches      int
	}{
		{"terminal", `package p

type Terminal struct{}
func (t Terminal) Local() bool { return true }
func (t Terminal) IsLocal() bool { return t.Local() }
`, nil, 0},
		{"time receiver", `package p
import "time"
func local(t time.Time) time.Time { return t.Local() }
`, nil, 1},
		{"unknown receiver", `package p
func local(t Unknown) bool { return t.Local() }
`, nil, 1},
		{"syntax error", `package p
func local(t Unknown) { t.Local() }
!
`, nil, 1},
		{"same line preserves time call", `package p
import "time"
type Terminal struct{}
func (t Terminal) Local() bool { return true }
func f(t Terminal) { _ = t.Local(); _ = time.Now().Local() }
`, nil, 1},
		{"still catches machine clock", `package p
import "time"
type Terminal struct{}
func (t Terminal) Local() bool { return true }
func f(t Terminal) { _ = t.Local(); _ = time.Now().Hour(); _ = time.Local }
`, nil, 2},
		{"bool method declared in another file", `package p
type Terminal struct{}
func f(t Terminal) bool { return t.Local() }
`, []string{`package p
type Terminal struct{}
func (t Terminal) Local() bool { return true }
`}, 0},
		{"another file does not hide the machine clock", `package p
import "time"
type Terminal struct{}
func f(t Terminal) bool { _ = time.Now().Local(); _ = time.Now().Hour(); _ = time.Local; return t.Local() }
`, []string{`package p
type Terminal struct{}
func (t Terminal) Local() bool { return true }
`}, 3},
		{"time method in another file stays visible", `package p
func f(t Stamp) { _ = t.Local() }
`, []string{`package p
import "time"
type Stamp struct{ time.Time }
`}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var siblings [][]byte
			for _, sibling := range c.siblings {
				siblings = append(siblings, []byte(sibling))
			}
			masked := maskBooleanLocalCalls([]byte(c.source), siblings...)
			if got := len(localZone.FindAll(masked, -1)); got != c.matches {
				t.Fatalf("matches=%d want=%d:\n%s", got, c.matches, masked)
			}
			if len(masked) != len(c.source) || strings.Count(string(masked), "\n") != strings.Count(c.source, "\n") {
				t.Fatal("masking changed source positions")
			}
		})
	}
}
