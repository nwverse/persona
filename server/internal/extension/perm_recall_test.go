package extension

import (
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

func recallLine(lines []PermissionLine) (PermissionLine, bool) {
	i := slices.IndexFunc(lines, func(l PermissionLine) bool { return l.Key == "recalls_sent" })
	if i < 0 {
		return PermissionLine{}, false
	}
	return lines[i], true
}

// recallConn is a connector of the example manifest that says it can take back what it sent (the capability recall), or not.
func recallConn(recalls bool, id, name string) map[string]any {
	caps := []any{"status"}
	if recalls {
		caps = append(caps, "recall")
	}
	return map[string]any{"id": id, "name": name, "capabilities": caps}
}

// A connector that can take back what it sent is shown on install with its name, in every language, and none is shown for
// one that cannot.
func TestRecallIsShownOnInstall(t *testing.T) {
	plain := withConnectors(t, recallConn(false, "examplechat", "Example Chat"))
	recalls := withConnectors(t, recallConn(true, "examplechat", "Example Chat"), recallConn(false, "examplemail", "Example Mail"))
	if got := recalls.Recalls(); len(got) != 1 || got[0].ID != "examplechat" {
		t.Fatalf("not read: %+v", got)
	}
	if _, ok := recallLine(Explain(plain)); ok {
		t.Errorf("a connector that does not say it can: %+v", Explain(plain))
	}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		line, ok := recallLine(ExplainIn(l, recalls))
		if !ok || line.Title == "" || !strings.Contains(line.Detail, "Example Chat") || strings.Contains(line.Detail, "Example Mail") ||
			strings.Contains(line.Title+line.Detail, "extension-permissions.") || strings.Contains(line.Detail, "{names}") {
			t.Errorf("%s: %+v", l, line)
		}
	}
	if line, _ := recallLine(Explain(recalls)); line.Title != i18n.T(i18n.Default, "extension-permissions.recalls_sent_title") {
		t.Errorf("title: %+v", line)
	}
}

// What an extension can take back is to be told, not agreed to (a recall happens only once the user asked for it on their
// own device, and nothing in it starts by itself): an upgrade that adds it is listed nowhere as added, asks nothing, and
// leaves the digest the user agreed to as it was; it is in the full list the upgrade shows. Keeping it or dropping it is no
// different.
func TestRecallAddedOnUpgradeAsksNothing(t *testing.T) {
	old := withConnectors(t, recallConn(false, "examplechat", "Example Chat"), recallConn(false, "examplemail", "Example Mail"))
	m := withConnectors(t, recallConn(true, "examplechat", "Example Chat"), recallConn(false, "examplemail", "Example Mail"))
	both := withConnectors(t, recallConn(true, "examplechat", "Example Chat"), recallConn(true, "examplemail", "Example Mail"))
	installed := func(x *Manifest) *Extension {
		return &Extension{Record: Record{Granted: x.Permissions, GrantedDigest: Digest(x)}, Manifest: x, Full: x}
	}
	for _, c := range []struct {
		what     string
		from, to *Manifest
	}{{"the first connector", old, m}, {"a second connector", m, both}, {"both at once", old, both}, {"dropping it", m, old}} {
		if got := Added(c.from, c.to); got != nil {
			t.Errorf("%s: listed as added: %+v", c.what, got)
		}
		for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
			if got := AddedIn(l, c.from, c.to); got != nil {
				t.Errorf("%s: %s: listed as added: %+v", c.what, l, got)
			}
		}
		if MorePermissions(installed(c.from), c.to) {
			t.Errorf("%s: asks for more", c.what)
		}
		if Digest(c.from) != Digest(c.to) {
			t.Errorf("%s: the digest the user agreed to changed", c.what)
		}
	}
	if _, ok := recallLine(Explain(m)); !ok {
		t.Error("the full list of what it can do after the upgrade does not have it")
	}
}
