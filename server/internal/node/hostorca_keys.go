package node

import (
	"strconv"
	"strings"
)

// Orca's terminal.send writes raw bytes, so Persona's key names ("enter", "shift+tab", "ctrl+u", "alt+left", "f5"…)
// become the bytes an xterm-compatible terminal sends for them. Modified cursor and function keys use xterm's
// parameter (1 + shift 1 + alt 2 + ctrl 4); alt alone on a character is ESC before it.

// orcaCursorKeys end a CSI sequence ("\x1b[A", modified "\x1b[1;3A").
var orcaCursorKeys = map[string]byte{"up": 'A', "down": 'B', "right": 'C', "left": 'D', "home": 'H', "end": 'F'}

// orcaTildeKeys are CSI <n> ~ ("\x1b[3~", modified "\x1b[3;5~").
var orcaTildeKeys = map[string]int{"insert": 2, "delete": 3, "pageup": 5, "pagedown": 6,
	"f5": 15, "f6": 17, "f7": 18, "f8": 19, "f9": 20, "f10": 21, "f11": 23, "f12": 24}

// orcaSS3Keys are F1–F4: SS3 P–S unmodified, CSI 1;<m> P–S modified.
var orcaSS3Keys = map[string]byte{"f1": 'P', "f2": 'Q', "f3": 'R', "f4": 'S'}

// orcaPlainKeys are keys whose bytes do not follow the xterm modifier scheme, by their full name. ctrl+enter has no
// legacy bytes: it is the CSI u form, which Claude Code reads as its send-now (#2467).
var orcaPlainKeys = map[string]string{
	"enter": "\r", "return": "\r", "shift+enter": "\x1b\r", "alt+enter": "\x1b\r", "ctrl+enter": "\x1b[13;5u", "ctrl+j": "\n",
	"esc": "\x1b", "escape": "\x1b", "alt+esc": "\x1b\x1b",
	"tab": "\t", "shift+tab": "\x1b[Z", "backtab": "\x1b[Z", "alt+tab": "\x1b\t",
	"backspace": "\x7f", "alt+backspace": "\x1b\x7f", "ctrl+backspace": "\x08",
	"space": " ", "ctrl+space": "\x00", "alt+space": "\x1b ",
}

// orcaKeyBytes is a key name's bytes; false for a key with no bytes Persona knows.
func orcaKeyBytes(k string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(k))
	if b, ok := orcaPlainKeys[name]; ok {
		return b, true
	}
	if r := []rune(k); len(r) == 1 && r[0] >= ' ' && r[0] != 0x7f {
		return k, true // a character as itself (case kept)
	}
	parts := strings.Split(name, "+")
	base := parts[len(parts)-1]
	shift, alt, ctrl := false, false, false
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "shift":
			shift = true
		case "alt", "option", "meta":
			alt = true
		case "ctrl", "control":
			ctrl = true
		default:
			return "", false
		}
	}
	if base == "" {
		return "", false
	}
	mod := 1
	if shift {
		mod++
	}
	if alt {
		mod += 2
	}
	if ctrl {
		mod += 4
	}
	if c, ok := orcaCursorKeys[base]; ok {
		if mod == 1 {
			return "\x1b[" + string(c), true
		}
		return "\x1b[1;" + strconv.Itoa(mod) + string(c), true
	}
	if n, ok := orcaTildeKeys[base]; ok {
		if mod == 1 {
			return "\x1b[" + strconv.Itoa(n) + "~", true
		}
		return "\x1b[" + strconv.Itoa(n) + ";" + strconv.Itoa(mod) + "~", true
	}
	if c, ok := orcaSS3Keys[base]; ok {
		if mod == 1 {
			return "\x1bO" + string(c), true
		}
		return "\x1b[1;" + strconv.Itoa(mod) + string(c), true
	}
	if b, ok := orcaPlainKeys[base]; ok && !shift && !ctrl && alt { // alt+enter, alt+tab… not in the table by name
		return "\x1b" + b, true
	}
	r := []rune(base)
	if len(r) != 1 || r[0] < ' ' || r[0] == 0x7f {
		return "", false
	}
	out := base
	if shift {
		out = strings.ToUpper(out)
	}
	if ctrl {
		c := r[0]
		switch {
		case c >= 'a' && c <= 'z':
			out = string(rune(c - 'a' + 1))
		case c >= '[' && c <= '_': // ctrl+[ esc, ctrl+\ , ctrl+] , ctrl+^ , ctrl+_
			out = string(rune(c - '@'))
		case c == '@' || c == '2':
			out = "\x00"
		default:
			return "", false
		}
	}
	if alt {
		out = "\x1b" + out
	}
	return out, true
}

// orcaTypes: the key's bytes type text into the program (a printable character, alone or after alt's ESC), as
// opposed to a control or navigation key.
func orcaTypes(b string) bool {
	b = strings.TrimPrefix(b, "\x1b")
	if b == "" || strings.HasPrefix(b, "[") && len(b) > 1 || strings.HasPrefix(b, "O") && len(b) == 2 {
		return false // an escape sequence (CSI / SS3)
	}
	for _, r := range b {
		if r >= ' ' && r != 0x7f {
			return true
		}
	}
	return false
}
