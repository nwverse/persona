package terminalmaintenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func fixture(t *testing.T, runtime string) (Record, Process) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := Record{Runtime: runtime, Version: "recorded-version", Session: testID,
		Pane: "test-pane", PID: 42, Executable: filepath.Join(dir, "runtime"),
		Cwd: dir, ProfileHome: dir, History: filepath.Join(dir, "history.jsonl"),
		Environment: map[string]string{"HOME": dir}}
	if runtime == "claude" {
		r.Environment["CLAUDE_CONFIG_DIR"] = dir
	} else {
		r.Environment["CODEX_HOME"] = dir
	}
	history := `{"sessionId":"` + testID + `","type":"user"}` + "\n"
	if runtime == "codex" {
		history = `{"type":"session_meta","payload":{"id":"` + testID + `"}}` + "\n"
	}
	write(t, r.Executable, "runtime bytes, never executed")
	write(t, r.History, history)
	return r, Process{PID: r.PID, UID: os.Getuid(), Identity: "test-process-observation"}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func prepare(t *testing.T, r Record, proc Process) Prepared {
	t.Helper()
	p, err := Prepare(r, proc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExactRecoveryRecipeNeverClaimsExecutionOrBinding(t *testing.T) {
	for _, runtime := range []string{"claude", "codex"} {
		t.Run(runtime, func(t *testing.T) {
			r, proc := fixture(t, runtime)
			r.ResumeOptions = []string{"--model", "recorded-model"}
			p := prepare(t, r, proc)
			recipe, err := p.Recipe()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--model", "recorded-model", "--resume", testID}
			if runtime == "codex" {
				args[2] = "resume"
			}
			if !reflect.DeepEqual(recipe.Arguments, args) || recipe.Cwd != r.Cwd ||
				recipe.Executable != r.Executable || !reflect.DeepEqual(recipe.Environment, r.Environment) {
				t.Fatalf("original recovery material changed: %+v", recipe)
			}
			if recipe.ExecutionSupported || p.Capabilities.ExitSupported || p.Capabilities.ResumeSupported ||
				p.Capabilities.BindingVerified || p.Capabilities.BackgroundComplete {
				t.Fatal("historical record became runtime authority")
			}
		})
	}
}

func TestPrepareRejectsUnknownOrConflictingRecoveryMaterial(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Record, *Process)
	}{
		{"unknown-runtime", func(r *Record, _ *Process) { r.Runtime = "other" }},
		{"candidate-id", func(r *Record, _ *Process) { r.Session = "candidate-not-bound" }},
		{"another-id", func(r *Record, _ *Process) { r.Session = "ffffffff-bbbb-cccc-dddd-eeeeeeeeeeee" }},
		{"missing-profile", func(r *Record, _ *Process) { r.ProfileHome = "" }},
		{"implicit-profile", func(r *Record, _ *Process) { delete(r.Environment, "CLAUDE_CONFIG_DIR") }},
		{"credential", func(r *Record, _ *Process) { r.Environment["API_KEY"] = "must-not-read" }},
		{"absent-pid", func(_ *Record, p *Process) { p.Identity = "" }},
		{"wrong-pid", func(_ *Record, p *Process) { p.PID++ }},
		{"wrong-owner", func(_ *Record, p *Process) { p.UID++ }},
		{"relative-cwd", func(r *Record, _ *Process) { r.Cwd = "." }},
		{"fork", func(r *Record, _ *Process) { r.ResumeOptions = []string{"--fork-session", "true"} }},
		{"new-session", func(r *Record, _ *Process) { r.ResumeOptions = []string{"--session-id", testID} }},
		{"continue", func(r *Record, _ *Process) { r.ResumeOptions = []string{"--continue", "true"} }},
		{"prompt", func(r *Record, _ *Process) { r.ResumeOptions = []string{"hello"} }},
		{"duplicate", func(r *Record, _ *Process) { r.ResumeOptions = []string{"--model", "a", "--model", "b"} }},
		{"flag-in-value", func(r *Record, _ *Process) { r.ResumeOptions = []string{"--model", "--fork-session"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, proc := fixture(t, "claude")
			test.change(&r, &proc)
			if _, err := Prepare(r, proc, time.Now()); err == nil {
				t.Fatal("accepted unsafe material")
			}
		})
	}
}

func TestRecoveryRefusesChangedOrMissingHistoryAndSettings(t *testing.T) {
	for _, target := range []string{"history", "executable", "settings", "deleted"} {
		t.Run(target, func(t *testing.T) {
			r, proc := fixture(t, "claude")
			settings := filepath.Join(r.ProfileHome, "settings.json")
			write(t, settings, `{}`)
			r.Settings = []string{settings}
			p := prepare(t, r, proc)
			switch target {
			case "history":
				write(t, r.History, `{"sessionId":"`+testID+`","type":"assistant"}`+"\n")
			case "executable":
				write(t, r.Executable, "different runtime")
			case "settings":
				write(t, settings, `{"model":"changed"}`)
			case "deleted":
				if err := os.Remove(r.History); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Recipe(); err == nil {
				t.Fatal("reused stale recovery material")
			}
		})
	}
}

func TestHistoryRejectsConflictPartialAndMissingIdentity(t *testing.T) {
	for _, history := range []string{
		`{"type":"user"}`,
		`{"sessionId":"` + testID + `"}` + "\n" + `{"sessionId":"ffffffff-bbbb-cccc-dddd-eeeeeeeeeeee"}`,
		`{"sessionId":"` + testID + `"}` + "\n" + `{"partial":`,
	} {
		r, proc := fixture(t, "claude")
		write(t, r.History, history)
		if _, err := Prepare(r, proc, time.Now()); err == nil {
			t.Fatal("accepted invalid history")
		}
	}
}

func TestPreparedCannotAssertNewCapabilities(t *testing.T) {
	r, proc := fixture(t, "claude")
	p := prepare(t, r, proc)
	p.Capabilities.BackgroundComplete = true
	if err := p.Validate(); err == nil {
		t.Fatal("accepted invented completeness")
	}
}

func TestPrivateJSONRejectsPublicUnknownAndExtraValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	for _, data := range []string{`{"unknown":true}`, `{} {}`, strings.Repeat(" ", (1<<20)+1)} {
		write(t, path, data)
		var r Record
		if err := readPrivateJSON(path, &r); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	r, _ := fixture(t, "claude")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(data))
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	var out Record
	if err := readPrivateJSON(path, &out); err == nil {
		t.Fatal("accepted publicly readable record")
	}
}
