package transcript

import "strings"

// The client an agent runs in, as the apps draw it beside the agent's mark (a </> badge on the command line's).
const (
	SurfaceDesktop = "desktop"
	SurfaceCLI     = "cli"
)

// ClaudeSurface is the client of a Claude Code transcript line's entrypoint: "cli" in a terminal, "claude-desktop"
// (and "claude-desktop-3p", the desktop app on a third-party provider) in Claude's desktop app. The SDK's ("sdk-cli",
// "sdk-ts"…) and anything newer say neither.
func ClaudeSurface(entrypoint string) string {
	switch {
	case entrypoint == "cli":
		return SurfaceCLI
	case strings.HasPrefix(entrypoint, "claude-desktop"):
		return SurfaceDesktop
	}
	return ""
}

// ClaudeProgram: a Claude Code transcript's entrypoint is a program's run of it, through an SDK ("sdk-cli" for `claude -p`,
// "sdk-ts", "sdk-py"…): a script or a tool at work, not a session of the user's.
func ClaudeProgram(entrypoint string) bool {
	return strings.HasPrefix(entrypoint, "sdk-")
}

// CodexSurface is the client of a Codex rollout's originator (its session_meta): "Codex Desktop" is the Codex App,
// "codex-tui" / "codex_cli_rs" the command line. `codex exec` and the SDKs over it say neither.
func CodexSurface(originator string) string {
	switch originator {
	case "Codex Desktop":
		return SurfaceDesktop
	case "codex-tui", "codex_cli_rs":
		return SurfaceCLI
	}
	return ""
}
