package proto

import "testing"

// A Windows node's paths read as that node wrote them on a hub of any system.
func TestNodePaths(t *testing.T) {
	for _, c := range []struct {
		in    string
		abs   bool
		clean string
	}{
		{`C:\Users\me\code\app`, true, `C:\Users\me\code\app`},
		{`C:\Users\me\code\app\`, true, `C:\Users\me\code\app`},
		{`c:/Users/me/code/app/`, true, `C:\Users\me\code\app`},
		{`C:\code\.\app\..\lib`, true, `C:\code\lib`},
		{`C:\..\..\code`, true, `C:\code`},
		{`C:\`, true, `C:\`},
		{`C:/`, true, `C:\`},
		{`\\server\share\me\code\`, true, `\\server\share\me\code`},
		{`/home/me/code/`, true, "/home/me/code"},
		{`/home/me/a\b`, true, `/home/me/a\b`}, // a backslash in a Unix name
		{`C:code`, false, "C:code"},            // relative to the drive's current directory
		{`code\app`, false, `code\app`},
		{"~/code", false, "~/code"},
		{"ws-one", false, "ws-one"},
	} {
		if got := NodeAbs(c.in); got != c.abs {
			t.Errorf("NodeAbs(%q) = %v, want %v", c.in, got, c.abs)
		}
		if got := NodeClean(c.in); got != c.clean {
			t.Errorf("NodeClean(%q) = %q, want %q", c.in, got, c.clean)
		}
	}
	for _, c := range [][3]string{
		{`C:\wt\app`, "fix-login", `C:\wt\app\fix-login`},
		{`C:\wt\app\`, "fix-login", `C:\wt\app\fix-login`},
		{`\\server\share\wt`, "fix-login", `\\server\share\wt\fix-login`},
		{"/wt/app", "fix-login", "/wt/app/fix-login"},
	} {
		if got := NodeJoin(c[0], c[1]); got != c[2] {
			t.Errorf("NodeJoin(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
