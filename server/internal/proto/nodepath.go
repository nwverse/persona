package proto

import (
	"path"
	"strings"
)

// A node reports paths in its own system's syntax, whatever system reads them: the hub and its managers may run on
// Linux or macOS while a Windows computer reports C:\Users\me\code. The path package knows only /, and path/filepath
// only the system it runs on, so both misread a Windows node's directories there (#7106). These read a path the way
// its node wrote it: a drive letter with a separator (C:\…, C:/…) or a share (\\server\share\…) is a Windows path,
// anything else a Unix one.

// WindowsPath: p is an absolute Windows path, a drive letter with a separator or a share.
func WindowsPath(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) < 3 || p[1] != ':' || p[2] != '\\' && p[2] != '/' {
		return false
	}
	c := p[0] | 0x20
	return 'a' <= c && c <= 'z'
}

// NodeAbs: p is an absolute directory on the node that reported it; path.IsAbs is false for a Windows one.
func NodeAbs(p string) bool {
	return path.IsAbs(p) || WindowsPath(p)
}

// NodeClean is path.Clean in the node's own syntax. A Windows path gets backslashes throughout, an upper-case drive
// letter and no trailing separator (a drive's root keeps its own, C:\), so one directory reads as one string however it
// was written (C:/code/app/, c:\code\app); .. stops at the drive or the share. Any other path is path.Clean's.
func NodeClean(p string) string {
	if !WindowsPath(p) {
		return path.Clean(p)
	}
	rest := strings.ReplaceAll(p, `\`, "/")
	var root string
	if strings.HasPrefix(rest, "//") {
		root, rest = `\\`, strings.TrimLeft(rest, "/")
	} else {
		root, rest = strings.ToUpper(rest[:1])+`:\`, rest[2:]
	}
	rest = strings.TrimPrefix(path.Clean("/"+rest), "/")
	return root + strings.ReplaceAll(rest, "/", `\`)
}

// NodeJoin is path.Join in the node's own syntax: name in the node's directory dir.
func NodeJoin(dir, name string) string {
	if !WindowsPath(dir) {
		return path.Join(dir, name)
	}
	return NodeClean(dir + `\` + name)
}
