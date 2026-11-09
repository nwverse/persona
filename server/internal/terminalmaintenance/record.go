// Package terminalmaintenance preserves recovery material without treating a
// terminal's departure, history or display state as authoritative idle evidence.
package terminalmaintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/nodeclient"
)

// Record is operator-supplied recovery material, not a runtime-issued binding.
// Environment deliberately excludes credentials and provider configuration.
type Record struct {
	Runtime       string            `json:"runtime"`
	Version       string            `json:"version"`
	Session       string            `json:"session_id"`
	Pane          string            `json:"pane"`
	PID           int               `json:"pid"`
	Executable    string            `json:"executable"`
	Cwd           string            `json:"cwd"`
	ProfileHome   string            `json:"profile_home"`
	History       string            `json:"history"`
	Environment   map[string]string `json:"environment"`
	ResumeOptions []string          `json:"resume_options"`
	Settings      []string          `json:"settings"`
}

type Prepared struct {
	Schema       int          `json:"schema"`
	PreparedAt   time.Time    `json:"prepared_at"`
	Record       Record       `json:"record"`
	Files        []File       `json:"files"`
	Process      Process      `json:"process"`
	Capabilities Capabilities `json:"capabilities"`
}

type Capabilities struct {
	NativeCommand      string `json:"native_command"`
	ExitSupported      bool   `json:"exit_supported"`
	ResumeSupported    bool   `json:"resume_supported"`
	BindingVerified    bool   `json:"binding_verified"`
	BackgroundComplete bool   `json:"background_complete"`
	Reason             string `json:"reason"`
}

// No supported CLI currently supplies a synchronous persisted-exit and complete
// background barrier here. Knowing the slash command is not that capability.
func capabilities() Capabilities {
	return Capabilities{NativeCommand: "/exit", Reason: "unsupported: no same-instance complete background, persisted-exit and recovery contract; recorded session identity is not a live binding"}
}

func (r Record) validate() error {
	if (r.Runtime != "claude" && r.Runtime != "codex") || !nodeclient.ValidSessionID(r.Session) ||
		r.Version == "" || r.Pane == "" || r.PID <= 0 {
		return errors.New("explicit runtime, version, exact session, pane and PID are required")
	}
	if len(r.Settings) > 16 {
		return errors.New("at most 16 selected settings files may be preserved")
	}
	seen := map[string]bool{}
	for _, path := range append([]string{r.Executable, r.History}, r.Settings...) {
		if seen[path] {
			return errors.New("duplicate recovery file")
		}
		seen[path] = true
	}
	for _, path := range append([]string{r.Executable, r.Cwd, r.ProfileHome, r.History}, r.Settings...) {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("recovery paths must be explicit canonical absolute paths")
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil || real != path {
			return errors.New("recovery path is unavailable or resolves to another path")
		}
	}
	for _, path := range []string{r.Cwd, r.ProfileHome} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return errors.New("recovery cwd and profile must exist as directories")
		}
	}
	profileKey := "CODEX_HOME"
	if r.Runtime == "claude" {
		profileKey = "CLAUDE_CONFIG_DIR"
	}
	if r.Environment[profileKey] != r.ProfileHome || r.Environment["HOME"] == "" {
		return errors.New("recorded HOME and explicit runtime profile are required; defaults are not inferred")
	}
	for k, v := range r.Environment {
		if k != "HOME" && k != profileKey {
			return errors.New("recovery environment allows only HOME and the runtime profile path")
		}
		if !filepath.IsAbs(v) || strings.ContainsRune(v, 0) {
			return errors.New("invalid recovery environment path")
		}
	}
	_, err := r.resumeArguments()
	return err
}

func (r Record) resumeArguments() ([]string, error) {
	// Only value-bearing, explicitly recorded options. No prompt, shell program,
	// config injection, fork, remote source, last/continue or new-session fallback.
	allowed := []string{"--model"}
	if r.Runtime == "codex" {
		allowed = append(allowed, "--profile", "--sandbox", "--ask-for-approval")
	} else {
		allowed = append(allowed, "--permission-mode", "--effort")
	}
	seen := map[string]bool{}
	for i := 0; i < len(r.ResumeOptions); i += 2 {
		key := r.ResumeOptions[i]
		if !slices.Contains(allowed, key) || seen[key] || i+1 == len(r.ResumeOptions) {
			return nil, errors.New("unsupported or duplicate recovery option")
		}
		value := r.ResumeOptions[i+1]
		if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
			return nil, errors.New("invalid recovery option value")
		}
		seen[key] = true
	}
	args := slices.Clone(r.ResumeOptions)
	if r.Runtime == "claude" {
		return append(args, "--resume", r.Session), nil
	}
	return append(args, "resume", r.Session), nil
}

// Prepare reads files and the explicit process only. It sends no commands, opens
// no runtime and writes no state. The caller must save stdout privately.
func Prepare(r Record, process Process, now time.Time) (Prepared, error) {
	out := Prepared{Schema: 1, PreparedAt: now.UTC(), Record: r, Process: process, Capabilities: capabilities()}
	if err := r.validate(); err != nil {
		return out, err
	}
	if process.PID != r.PID || process.UID != os.Getuid() || process.Identity == "" {
		return out, errors.New("recorded PID is absent or its owner is unavailable")
	}
	for _, path := range append([]string{r.Executable, r.History}, r.Settings...) {
		f, err := snapshot(path, r)
		if err != nil {
			return out, fmt.Errorf("recovery file: %w", err)
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

// Validate rereads every pinned file. Native history may append only if the
// original byte prefix survives and all session-bearing rows retain the ID.
func (p Prepared) Validate() error {
	if p.Schema != 1 || p.PreparedAt.IsZero() || p.Capabilities != capabilities() ||
		p.Process.PID != p.Record.PID || p.Process.UID != os.Getuid() || p.Process.Identity == "" {
		return errors.New("invalid maintenance record or unsupported capability assertion")
	}
	if err := p.Record.validate(); err != nil {
		return err
	}
	paths := append([]string{p.Record.Executable, p.Record.History}, p.Record.Settings...)
	if len(p.Files) != len(paths) {
		return errors.New("incomplete recovery file set")
	}
	for i, path := range paths {
		prefix := int64(0)
		if i == 1 {
			prefix = p.Files[i].Size
			if prefix <= 0 || p.Files[i].SHA256 == "" {
				return errors.New("missing original history prefix")
			}
		}
		f, err := snapshotPrefix(path, p.Record, prefix)
		if err != nil {
			return err
		}
		if i == 1 && f.Size >= p.Files[i].Size {
			f.Size = p.Files[i].Size
		}
		if f != p.Files[i] {
			return errors.New("recovery file changed; prepare again before considering restoration")
		}
	}
	return nil
}

type RecoveryRecipe struct {
	Executable         string            `json:"executable"`
	Arguments          []string          `json:"arguments"`
	Cwd                string            `json:"cwd"`
	Environment        map[string]string `json:"environment"`
	ExecutionSupported bool              `json:"execution_supported"`
	Reason             string            `json:"reason"`
}

// Recipe describes exact-ID recovery for review. It never executes a launcher,
// creates a session or promotes a historical ID into a current process binding.
func (p Prepared) Recipe() (RecoveryRecipe, error) {
	if err := p.Validate(); err != nil {
		return RecoveryRecipe{}, err
	}
	args, err := p.Record.resumeArguments()
	if err != nil {
		return RecoveryRecipe{}, err
	}
	return RecoveryRecipe{Executable: p.Record.Executable, Arguments: args, Cwd: p.Record.Cwd,
		Environment: p.Record.Environment, Reason: capabilities().Reason}, nil
}
