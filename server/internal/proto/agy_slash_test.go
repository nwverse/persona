package proto

import (
	"strings"
	"testing"
)

func TestAgySlashCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"bare boost", "/boost", "/boost"},
		{"boost with arg", "/boost fix the auth race", "/boost"},
		{"boost multiline", "/boost\nline one\nline two", "/boost"},
		{"boost with args multiline", "/boost fix auth\nkeep tests green", "/boost"},
		{"boost tab separated", "/boost\tfix auth", "/boost"},
		{"boost crlf", "/boost\r\nline two", "/boost"},
		{"boost formfeed", "/boost\fline two", "/boost"},
		{"boost vertical tab", "/boost\vline two", "/boost"},
		{"boost uppercase", "/BOOST Fix It", "/boost"},
		{"boost leading ascii whitespace", " \n\t\f\v/boost investigate", "/boost"},
		{"teamwork", "/teamwork split backend and frontend\nreport back", "/teamwork"},
		{"swarm", "/swarm coordinate three workers\nverify before merge", "/swarm"},
		{"goal", "/goal finish migration overnight\nrun full suite", "/goal"},
		{"plan", "/plan design the schema\nkeep backwards compat", "/plan"},
		{"grill-me", "/grill-me review the rollout plan\nchallenge assumptions", "/grill-me"},
		{"learn", "/learn remember this build command", "/learn"},
		{"schedule", "/schedule check status every morning", "/schedule"},
		{"browser", "/browser open localhost:3000 and verify", "/browser"},
		// Negative controls: control commands, filesystem paths, unknown commands, mid-text commands, non-ASCII parity guards.
		{"compact is not in prompt allowlist", "/compact", ""},
		{"status is not in prompt allowlist", "/status", ""},
		{"model is not in prompt allowlist", "/model gemini", ""},
		{"unix path single line", "/Users/foo/bar", ""},
		{"unix path multiline", "/Users/foo/bar\nplease read this file", ""},
		{"tmp path multiline", "/tmp/report.md\nreview section 2", ""},
		{"slash command prefix as path", "/plan/spec.md\nplease review", ""},
		{"slash command suffix hyphen", "/boost-extra\nrun this", ""},
		{"unknown multiline command", "/unknown\nline two", ""},
		{"mid-sentence command", "please run /boost\non this task", ""},
		{"kelvin sign does not fold to k", "/teamwor\u212a fix", ""},
		{"long s does not fold to s", "/boo\u017ft fix", ""},
		{"leading NEL rejected", "\u0085/boost fix", ""},
		{"leading BOM rejected", "\ufeff/boost fix", ""},
		{"leading NBSP rejected", "\u00a0/boost fix", ""},
		{"NBSP delimiter rejected", "/boost\u00a0fix", ""},
		{"empty", "", ""},
		{"whitespace only", "   \n\t ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AgySlashCommand(tc.in)
			if got != tc.want {
				t.Fatalf("AgySlashCommand(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if HasAgySlashCommand(tc.in) != (tc.want != "") {
				t.Fatalf("HasAgySlashCommand(%q) = %v, want %v", tc.in, HasAgySlashCommand(tc.in), tc.want != "")
			}
		})
	}
}

func TestPrependPreservingAgySlashCommand(t *testing.T) {
	const preamble = "<persona-context>\nproject background\n</persona-context>"

	t.Run("ordinary prompt keeps prepend order", func(t *testing.T) {
		got := PrependPreservingAgySlashCommand(preamble, "fix the bug\nwith tests")
		want := preamble + "\n\nfix the bug\nwith tests"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("non-allowlisted slash path keeps prepend order", func(t *testing.T) {
		prompt := "/Users/foo/bar\nplease inspect this path"
		got := PrependPreservingAgySlashCommand(preamble, prompt)
		want := preamble + "\n\n" + prompt
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("allowlisted slash command stays at byte 0 and preserves preamble", func(t *testing.T) {
		prompt := "  \n/boost fix the race\nkeep existing behavior"
		got := PrependPreservingAgySlashCommand(preamble, prompt)
		if !strings.HasPrefix(got, "/boost fix the race\nkeep existing behavior") {
			t.Fatalf("slash command not at byte 0: %q", got)
		}
		if !strings.Contains(got, preamble) {
			t.Fatalf("preamble dropped: %q", got)
		}
		want := "/boost fix the race\nkeep existing behavior\n\n" + preamble
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("preserves single trailing attached_images at the very end", func(t *testing.T) {
		prompt := "/swarm check screenshot\n\n<attached_images>\n/tmp/a.png\n</attached_images>"
		got := PrependPreservingAgySlashCommand(preamble, prompt)
		want := "/swarm check screenshot\n\n" + preamble + "\n\n<attached_images>\n/tmp/a.png\n</attached_images>"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("preserves multiple trailing attached_images blocks at the very end", func(t *testing.T) {
		prompt := "/swarm check screenshots\n\n<attached_images>\n/tmp/a.png\n</attached_images>\n\n<attached_images>\n/tmp/b.png\n</attached_images>"
		got := PrependPreservingAgySlashCommand(preamble, prompt)
		want := "/swarm check screenshots\n\n" + preamble + "\n\n<attached_images>\n/tmp/a.png\n</attached_images>\n\n<attached_images>\n/tmp/b.png\n</attached_images>"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("does not split inline prose mentioning attached_images", func(t *testing.T) {
		for _, prompt := range []string{
			"/boost explain why <attached_images>/tmp/a.png</attached_images>",
			"/boost explain\nsee inline <attached_images>/tmp/a.png</attached_images>",
		} {
			got := PrependPreservingAgySlashCommand(preamble, prompt)
			want := prompt + "\n\n" + preamble
			if got != want {
				t.Fatalf("inline prose split for %q:\ngot:  %q\nwant: %q", prompt, got, want)
			}
		}
	})

	t.Run("empty preamble trims leading whitespace for allowlisted command", func(t *testing.T) {
		got := PrependPreservingAgySlashCommand("  ", " /goal finish migration")
		if got != "/goal finish migration" {
			t.Fatalf("got %q", got)
		}
	})
}
