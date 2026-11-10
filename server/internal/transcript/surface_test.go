package transcript

import "testing"

func TestSurfaces(t *testing.T) {
	for in, want := range map[string]string{"cli": SurfaceCLI, "claude-desktop": SurfaceDesktop, "claude-desktop-3p": SurfaceDesktop,
		"sdk-cli": "", "sdk-ts": "", "": ""} {
		if got := ClaudeSurface(in); got != want {
			t.Errorf("ClaudeSurface(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"Codex Desktop": SurfaceDesktop, "codex-tui": SurfaceCLI, "codex_cli_rs": SurfaceCLI,
		"codex_exec": "", "codex_sdk_ts": "", "": ""} {
		if got := CodexSurface(in); got != want {
			t.Errorf("CodexSurface(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLineReadsTheSurface(t *testing.T) {
	var m Meta
	ParseLine(0, []byte(`{"type":"user","uuid":"a","message":{"role":"user","content":"hi"},"entrypoint":"claude-desktop"}`), &m)
	if m.Surface != SurfaceDesktop {
		t.Fatalf("Claude desktop line: surface %q", m.Surface)
	}
	m = Meta{}
	ParseLine(0, []byte(`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"t","cwd":"/w","originator":"codex-tui","cli_version":"1"}}`), &m)
	if m.Surface != SurfaceCLI {
		t.Fatalf("Codex CLI rollout: surface %q", m.Surface)
	}
}

// A program's run (an SDK, `claude -p`) by its entrypoint; the latest line's wins, so one resumed in a terminal is not.
func TestClaudeProgramByTheLatestEntrypoint(t *testing.T) {
	for in, want := range map[string]bool{"sdk-cli": true, "sdk-ts": true, "sdk-py": true, "cli": false, "claude-desktop": false, "": false} {
		if got := ClaudeProgram(in); got != want {
			t.Errorf("ClaudeProgram(%q) = %v, want %v", in, got, want)
		}
	}
	var m Meta
	ParseLine(0, []byte(`{"type":"user","uuid":"a","message":{"role":"user","content":"hi"},"entrypoint":"sdk-cli"}`), &m)
	ParseLine(1, []byte(`{"type":"user","uuid":"b","message":{"role":"user","content":"go on"},"entrypoint":"cli"}`), &m)
	if m.Entrypoint != "cli" || m.Surface != SurfaceCLI {
		t.Fatalf("entrypoint %q, surface %q", m.Entrypoint, m.Surface)
	}
}
