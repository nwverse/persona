package node

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macOS refusing the protected folders (TCC) says what to do; other errors and other folders pass through.
func TestExplainDenied(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "/Users/me/Desktop/a.png", Err: fs.ErrPermission}
	err := explainDenied("/Users/me/Desktop/a.png", "/Users/me", denied)
	if !strings.Contains(err.Error(), "「桌面」") || !strings.Contains(err.Error(), "完全磁盘访问权限") || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Desktop: %v", err)
	}
	if err := explainDenied("/Users/me/Downloads", "/Users/me", denied); !strings.Contains(err.Error(), "「下载」") {
		t.Fatalf("Downloads itself: %v", err)
	}
	for _, p := range []string{"/Users/me/code/a.png", "/Users/user/Desktop/a.png", "/Users/me"} {
		if err := explainDenied(p, "/Users/me", denied); err != denied {
			t.Fatalf("%s: rewrote %v", p, err)
		}
	}
	missing := &fs.PathError{Op: "open", Path: "/Users/me/Desktop/a.png", Err: fs.ErrNotExist}
	if err := explainDenied("/Users/me/Desktop/a.png", "/Users/me", missing); err != missing {
		t.Fatalf("not found: rewrote %v", err)
	}
}

// Who gets Full Disk Access depends on what started the node (#5944): TCC holds an ssh-started node's reads to
// sshd-keygen-wrapper, a LaunchAgent's to its own program.
func TestExplainDeniedSaysWhoToAllow(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "/Users/me/Documents/a.md", Err: fs.ErrPermission}
	for via, want := range map[string]string{
		"ssh":     "sshd-keygen-wrapper",
		"launchd": "加上 /Users/me/.persona/bin/personad 并打开它（Persona 的 node 由 launchd 服务启动，macOS 按这个服务运行的程序给权限",
		"":        "运行 Persona node 的终端或 App",
	} {
		err := explainDeniedFor("/Users/me/Documents/a.md", "/Users/me", via, "/Users/me/.persona/bin/personad", denied)
		if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "「文稿」") || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%q: %v", via, err)
		}
		if via != "ssh" && strings.Contains(err.Error(), "sshd-keygen-wrapper") {
			t.Errorf("%q names ssh: %v", via, err)
		}
	}
	if err := explainDeniedFor("/Users/me/Documents/a.md", "/Users/me", "launchd", "", denied); !strings.Contains(err.Error(), "加上 personad") {
		t.Errorf("no executable: %v", err)
	}
	if err := explainDeniedFor("/Users/me/code/a.md", "/Users/me", "launchd", "/x", denied); err != denied {
		t.Errorf("outside the protected folders: %v", err)
	}
}

// Under launchd, TCC holds the node to the program of the agent it runs under (#5944): its own agent's (persona-node
// or personad), or the built-in manager's agent's for the node of the hub's computer, which that manager starts.
func TestFullDiskAccessNamesTheAgentsProgram(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(label, body string) {
		t.Helper()
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>` + label + `</string>
` + body + `
</dict></plist>
`
		if err := os.WriteFile(filepath.Join(dir, label+".plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := "/Users/me/.persona/bin/persona-node"
	write("com.persona.manager.assistant", `  <key>ProgramArguments</key><array><string>/Users/me/.persona/bin/persona-manager</string><string>assistant</string></array>`)
	write("com.persona.node", `  <key>ProgramArguments</key>
  <array>
    <string>~/.persona/bin/persona-node</string>
    <string>node</string>
  </array>`)
	write("com.example.program", `  <key>Program</key><string>/opt/a&amp;b/personad</string>
  <key>ProgramArguments</key><array><string>ignored</string></array>`)
	write("com.example.none", `  <key>RunAtLoad</key><true/>`)
	for _, c := range []struct{ via, label, want string }{
		{"launchd", "com.persona.manager.assistant", "/Users/me/.persona/bin/persona-manager"},
		{"launchd", "com.persona.node", filepath.Join(home, ".persona", "bin", "persona-node")},
		{"launchd", "com.example.program", "/opt/a&b/personad"},
		{"launchd", "com.example.none", exe},
		{"launchd", "com.example.missing", exe},
		{"launchd", "../com.persona.node", exe},
		{"launchd", "", exe},
		{"ssh", "com.persona.manager.assistant", exe},
		{"", "com.persona.manager.assistant", exe},
	} {
		if got := fullDiskAccessProgram(c.via, home, c.label, exe); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.via, c.label, got, c.want)
		}
	}
	denied := &fs.PathError{Op: "open", Path: "/Users/me/Documents/a.md", Err: fs.ErrPermission}
	err := explainDeniedFor("/Users/me/Documents/a.md", "/Users/me", "launchd",
		fullDiskAccessProgram("launchd", home, "com.persona.manager.assistant", exe), denied)
	if !strings.Contains(err.Error(), "加上 /Users/me/.persona/bin/persona-manager 并打开它") {
		t.Errorf("manager's node: %v", err)
	}
}
