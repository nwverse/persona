package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/nodeclient"
)

// Opt-in report-only hooks for the claude sessions Persona did not start (hub.json machines[].claude_global_hooks,
// docs/integrations/claude-integration.md "Global report-only hooks"). Sessions Persona starts load the persona-hooks plugin for that
// session alone (hooksplugin.go); the ones the user starts in a herdr pane themselves have no hooks, so the node reads
// the screen and the transcript for them. These entries go into the user's own settings.json, where hooks of every
// tool add up, so they differ from the plugin's in two ways that matter:
//
//   - They never block. Other tools already put synchronous PermissionRequest hooks there; one more would stack the
//     approvals, and one that waits for the phone for 24 hours would leave the person at the computer unable to answer
//     in the terminal (the reason docs/integrations/codex.md rejects Codex's PermissionRequest hook). Every entry is async with a
//     short timeout, and there is no PermissionRequest, Elicitation or blocking PreToolUse among them: answers from
//     the phone for these sessions keep going through the screen card.
//   - Every entry is tagged, so install and uninstall touch nothing else. The tag is the command itself: it ends in
//     `hook --persona-global`, which no other tool's command has.
//
// CLAUDE_CODE_PLUGIN_DIRS (a list of absolute folders, separated like PATH) is the other way in and was not chosen:
// it is one variable, and a machine that already has another tool's folder in it (in the settings `env` block, which
// is where such tools put it) would need that tool's value edited.

// globalHookFlag is the argument that makes `personad hook` report only; it is also what marks an entry as Persona's.
const globalHookFlag = nodeclient.GlobalHookFlag

// globalHookTimeout is how long Claude Code lets a report-only hook run (seconds); the hook itself gives up sooner.
const globalHookTimeout = 10

// hookEntry and hookGroup are one settings.json hook and the matcher group around it, in the order Claude Code's own
// documentation writes them.
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
	Async   bool   `json:"async"`
}

type hookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

// globalHookEvents are the events that are reported, in the order they are written. AskUserQuestion and ExitPlanMode
// before and after tell the hub a card is up or answered; nothing else about tools is reported.
var globalHookEvents = []struct{ Event, Matcher string }{
	{"SessionStart", ""},
	{"UserPromptSubmit", ""},
	{"Stop", ""},
	{"Notification", ""},
	{"PreToolUse", "AskUserQuestion"},
	{"PostToolUse", "AskUserQuestion"},
	{"PreToolUse", "ExitPlanMode"},
	{"PostToolUse", "ExitPlanMode"},
}

// globalHookCommand is the command of every entry: the node's own binary in report-only mode.
func globalHookCommand(bin string) string { return shellQuote(bin) + " hook " + globalHookFlag }

// isGlobalHookCommand says whether a hook command is one of Persona's report-only entries.
func isGlobalHookCommand(cmd string) bool {
	f := strings.Fields(cmd)
	return len(f) >= 3 && f[len(f)-2] == "hook" && f[len(f)-1] == globalHookFlag
}

// applyGlobalHooks makes the machine's default claude config directory match the hub's word: on installs the entries,
// off removes the ones Persona put there (and does nothing, not even a write, when there are none).
func (n *Node) applyGlobalHooks(on bool) {
	if n.sessionScope != nil {
		return
	}
	dir := n.defaultClaudeDir()
	if _, err := os.Stat(dir); err != nil {
		return // no claude here
	}
	path := settingsFile(dir)
	var changed bool
	var err error
	if on {
		changed, err = installGlobalHooks(path, globalHookCommand(n.bin))
	} else {
		changed, err = uninstallGlobalHooks(path)
	}
	switch {
	case err != nil:
		n.logf("claude global hooks (%s): %v", path, err)
	case changed:
		n.logf("claude global hooks %s in %s", map[bool]string{true: "installed", false: "removed"}[on], path)
	}
}

// installGlobalHooks adds Persona's entries to the settings file at path (created when missing), replacing the ones it
// added before, and returns whether the file changed. Nothing else moves: other keys and other tools' hooks keep their
// order and values; only the layout is normalised (one indent unit, taken from the file) because the file is written
// back through a JSON encoder. A copy of the old file is kept first. A file that is not a JSON object is left alone.
func installGlobalHooks(path, cmd string) (bool, error) {
	return editSettings(path, true, func(top *jsonObject) (bool, error) {
		hooks, err := hooksMember(top, true)
		if err != nil {
			return false, err
		}
		changed := false
		done := map[string]bool{}
		for _, e := range globalHookEvents {
			if done[e.Event] {
				continue
			}
			done[e.Event] = true
			var want []json.RawMessage
			for _, g := range globalHookEvents {
				if g.Event != e.Event {
					continue
				}
				b, err := marshalPlain(hookGroup{Matcher: g.Matcher, Hooks: []hookEntry{{Type: "command", Command: cmd, Timeout: globalHookTimeout, Async: true}}})
				if err != nil {
					return false, err
				}
				want = append(want, b)
			}
			groups, err := hooks.groups(e.Event)
			if err != nil {
				return false, err
			}
			rest, owned, stripped := splitOwned(groups)
			if !stripped && sameGroups(owned, want) {
				continue // already as it should be: leave the file's layout alone too
			}
			hooks.set(e.Event, append(rest, want...))
			changed = true
		}
		if changed {
			if err := setHooksMember(top, hooks); err != nil {
				return false, err
			}
		}
		return changed, nil
	})
}

// uninstallGlobalHooks removes the entries Persona added and returns whether it did; a file without them is not
// rewritten. Events and the "hooks" key that end up empty because of it go too.
func uninstallGlobalHooks(path string) (bool, error) {
	return editSettings(path, false, func(top *jsonObject) (bool, error) {
		hooks, err := hooksMember(top, false)
		if err != nil || hooks == nil {
			return false, err
		}
		changed := false
		for _, m := range append(jsonObject(nil), *hooks...) {
			groups, err := hooks.groups(m.Key)
			if err != nil {
				continue // an event written in a shape that is not a list is not ours
			}
			rest, owned, stripped := splitOwned(groups)
			if len(owned) == 0 && !stripped {
				continue
			}
			changed = true
			if len(rest) == 0 {
				hooks.remove(m.Key)
			} else {
				hooks.set(m.Key, rest)
			}
		}
		if !changed {
			return false, nil
		}
		if len(*hooks) == 0 {
			top.remove("hooks")
			return true, nil
		}
		return true, setHooksMember(top, hooks)
	})
}

// splitOwned sorts an event's groups: rest are the ones that are not Persona's (a group the user added to Persona's
// own, with a hook of theirs beside ours, stays without ours: stripped), owned are Persona's alone.
func splitOwned(groups []json.RawMessage) (rest, owned []json.RawMessage, stripped bool) {
	for _, g := range groups {
		kept, mine, ok := stripGroup(g)
		switch {
		case !ok || mine == 0:
			rest = append(rest, g)
		case kept == nil:
			owned = append(owned, g)
		default:
			rest = append(rest, kept)
			stripped = true
		}
	}
	return rest, owned, stripped
}

// stripGroup takes Persona's hooks out of one matcher group: mine is how many it had, kept the group without them
// (nil when nothing else was in it). ok is false for a group that does not look like one.
func stripGroup(g json.RawMessage) (kept json.RawMessage, mine int, ok bool) {
	var obj jsonObject
	if json.Unmarshal(g, &obj) != nil {
		return nil, 0, false
	}
	raw, found := obj.get("hooks")
	if !found {
		return nil, 0, true
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil, 0, false
	}
	var left []json.RawMessage
	for _, h := range list {
		var e struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(h, &e) == nil && isGlobalHookCommand(e.Command) {
			mine++
			continue
		}
		left = append(left, h)
	}
	if mine == 0 {
		return nil, 0, true
	}
	if len(left) == 0 {
		return nil, mine, true
	}
	b, err := marshalPlain(left)
	if err != nil {
		return nil, 0, false
	}
	obj.set("hooks", json.RawMessage(b))
	out, err := marshalPlain(obj)
	if err != nil {
		return nil, 0, false
	}
	return out, mine, true
}

// sameGroups says whether two lists of groups are the same JSON, whatever their whitespace.
func sameGroups(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var x, y bytes.Buffer
		if json.Compact(&x, a[i]) != nil || json.Compact(&y, b[i]) != nil || !bytes.Equal(x.Bytes(), y.Bytes()) {
			return false
		}
	}
	return true
}

// editSettings reads the settings file at path, lets edit change its top-level object, and writes it back when edit
// says it changed something: a copy of the old file first, then a temporary file in the same directory renamed over
// it. path is the real file (settingsFile follows a symlink, as dotfile managers make). create: a missing file is started as {}.
func editSettings(path string, create bool, edit func(top *jsonObject) (bool, error)) (bool, error) {
	orig, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if !create {
			return false, nil
		}
		orig = nil
	case err != nil:
		return false, err
	}
	var top jsonObject
	if len(bytes.TrimSpace(orig)) > 0 {
		if err := json.Unmarshal(orig, &top); err != nil {
			return false, fmt.Errorf("not a JSON object, left alone: %w", err)
		}
	}
	changed, err := edit(&top)
	if err != nil || !changed {
		return false, err
	}
	body, err := marshalPlain(top)
	if err != nil {
		return false, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", indentOf(orig)); err != nil {
		return false, err
	}
	if len(orig) == 0 || bytes.HasSuffix(orig, []byte("\n")) {
		out.WriteByte('\n')
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if len(orig) > 0 {
		bak := path + ".persona-bak-" + time.Now().UTC().Format("20060102T150405Z")
		if err := os.WriteFile(bak, orig, mode); err != nil {
			return false, fmt.Errorf("backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-persona-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// indentOf is the file's own indent unit (its first indented line), two spaces when it has none.
func indentOf(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			return line[:len(line)-len(trimmed)]
		}
	}
	return "  "
}

// marshalPlain encodes v without escaping <, > and & (commands have && and redirects), as Claude Code writes them.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// jsonObject is a JSON object that keeps its members in the order the file had them (a Go map would sort them).
type jsonObject []jsonMember

type jsonMember struct {
	Key string
	Val json.RawMessage
}

func (o *jsonObject) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil {
		return err
	} else if d, ok := t.(json.Delim); !ok || d != '{' {
		return errors.New("not an object")
	}
	*o = (*o)[:0]
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		*o = append(*o, jsonMember{Key: kt.(string), Val: v})
	}
	return nil
}

func (o jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := marshalPlain(m.Key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.Val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o jsonObject) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == key {
			return m.Val, true
		}
	}
	return nil, false
}

// set replaces the member in place, or appends it.
func (o *jsonObject) set(key string, val any) {
	var raw json.RawMessage
	switch v := val.(type) {
	case json.RawMessage:
		raw = v
	default:
		b, err := marshalPlain(val)
		if err != nil {
			return
		}
		raw = b
	}
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Val = raw
			return
		}
	}
	*o = append(*o, jsonMember{Key: key, Val: raw})
}

func (o *jsonObject) remove(key string) {
	out := (*o)[:0]
	for _, m := range *o {
		if m.Key != key {
			out = append(out, m)
		}
	}
	*o = out
}

// groups is the list of matcher groups of one event; none when the event is not there.
func (o jsonObject) groups(event string) ([]json.RawMessage, error) {
	raw, ok := o.get(event)
	if !ok {
		return nil, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("hooks.%s is not a list, left alone", event)
	}
	return list, nil
}

// hooksMember is the settings' "hooks" object; nil (and no error) when it is not there and create is false.
func hooksMember(top *jsonObject, create bool) (*jsonObject, error) {
	raw, ok := top.get("hooks")
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		if !create {
			return nil, nil
		}
		return &jsonObject{}, nil
	}
	var hooks jsonObject
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return nil, fmt.Errorf(`"hooks" is not an object, left alone: %w`, err)
	}
	return &hooks, nil
}

func setHooksMember(top *jsonObject, hooks *jsonObject) error {
	b, err := marshalPlain(hooks)
	if err != nil {
		return err
	}
	top.set("hooks", json.RawMessage(b))
	return nil
}
