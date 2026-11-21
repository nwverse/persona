package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type fakeDirEntry struct {
	name string
	dir  bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.dir }
func (f fakeDirEntry) Type() os.FileMode          { return os.ModeDir }
func (f fakeDirEntry) Info() (os.FileInfo, error) { return fakeFileInfo{name: f.name, dir: f.dir}, nil }

type fakeFileInfo struct {
	name string
	dir  bool
}

func (f fakeFileInfo) Name() string { return f.name }
func (f fakeFileInfo) Size() int64  { return 0 }
func (f fakeFileInfo) Mode() os.FileMode {
	if f.dir {
		return os.ModeDir | 0o755
	}
	return 0o755
}
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

func TestMachineToolchainProbeCoreAgentsDockerGHHardwareAndRedaction(t *testing.T) {
	home := "/home/dev"
	nvmNodeBin := filepath.Join(home, ".nvm", "versions", "node", "v22.14.0", "bin")
	customGoBin := "/usr/local/go/bin"

	dirs := map[string]bool{
		"/usr/bin":                           true,
		"/usr/local/bin":                     true,
		customGoBin:                          true,
		nvmNodeBin:                           true,
		filepath.Join(home, ".local", "bin"): true,
		filepath.Join(home, ".local", "share", "mise", "shims"): true,
	}
	bins := map[string]string{
		"git":        "/usr/bin/git",
		"go":         filepath.Join(customGoBin, "go"),
		"node":       filepath.Join(nvmNodeBin, "node"),
		"npm":        filepath.Join(nvmNodeBin, "npm"),
		"pnpm":       filepath.Join(nvmNodeBin, "pnpm"),
		"python3":    "/usr/bin/python3",
		"gh":         "/usr/local/bin/gh",
		"docker":     "/usr/bin/docker",
		"pg_dump":    "/usr/bin/pg_dump",
		"psql":       "/usr/bin/psql",
		"claude":     filepath.Join(home, ".local", "bin", "claude"),
		"codex":      filepath.Join(nvmNodeBin, "codex"),
		"agy":        filepath.Join(home, ".local", "bin", "agy"),
		"nvidia-smi": "/usr/bin/nvidia-smi",
	}
	symlinks := map[string]string{
		filepath.Join(home, ".local", "bin", "claude"): "/opt/claude/bin/claude-real",
	}
	files := map[string]string{
		"/sys/class/power_supply/BAT0/type":     "Battery\n",
		"/sys/class/power_supply/BAT0/capacity": "84\n",
		"/sys/class/power_supply/BAT0/status":   "Charging\n",
		"/sys/class/power_supply/AC/type":       "Mains\n",
		"/sys/class/power_supply/AC/online":     "1\n",
		"/sys/power/mem_sleep":                  "s2idle [deep]\n",
	}

	envMap := map[string]string{
		"PATH":           "/usr/bin:/usr/local/bin",
		"SHELL":          "/bin/zsh",
		"HTTPS_PROXY":    "http://user:supersecretpass@proxy.example.com:8080",
		"GITHUB_TOKEN":   "ghp_1234567890abcdef1234567890abcdef",
		"OPENAI_API_KEY": "sk-proj-secretvalue9876543210",
		"GOPATH":         "/home/dev/go",
	}

	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	n := &Node{home: home}
	n.toolchain.deps = &toolchainDeps{
		goos:   "linux",
		goarch: "amd64",
		home:   home,
		getenv: func(k string) string { return envMap[k] },
		now:    func() time.Time { return now },
		stat: func(p string) (os.FileInfo, error) {
			if dirs[p] {
				return fakeFileInfo{name: filepath.Base(p), dir: true}, nil
			}
			if p == "/var/run/docker.sock" {
				return fakeFileInfo{name: "docker.sock", dir: false}, nil
			}
			return nil, os.ErrNotExist
		},
		readDir: func(p string) ([]os.DirEntry, error) {
			switch p {
			case filepath.Join(home, ".nvm", "versions", "node"):
				return []os.DirEntry{
					fakeDirEntry{name: "v20.11.0", dir: true},
					fakeDirEntry{name: "v22.14.0", dir: true},
				}, nil
			case "/sys/class/power_supply":
				return []os.DirEntry{
					fakeDirEntry{name: "BAT0", dir: true},
					fakeDirEntry{name: "AC", dir: true},
				}, nil
			default:
				return nil, os.ErrNotExist
			}
		},
		readFile: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, os.ErrNotExist
		},
		evalSymlinks: func(p string) (string, error) {
			if target, ok := symlinks[p]; ok {
				return target, nil
			}
			return p, nil
		},
		lookPathIn: func(name string, searchDirs []string) string {
			p, ok := bins[name]
			if !ok {
				return ""
			}
			if slices.Contains(searchDirs, filepath.Dir(p)) {
				return p
			}
			return ""
		},
		dialUnix: func(_ context.Context, sock string) error {
			if sock == "/var/run/docker.sock" {
				return nil
			}
			return os.ErrNotExist
		},
		run: func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
			base := filepath.Base(name)
			switch base {
			case "git":
				return []byte("git version 2.47.1\n"), nil
			case "go":
				return []byte("go version go1.24.2 linux/amd64\n"), nil
			case "node":
				return []byte("v22.14.0\n"), nil
			case "npm":
				return []byte("10.9.2\n"), nil
			case "pnpm":
				return []byte("9.15.4\n"), nil
			case "python3":
				return []byte("Python 3.12.7\n"), nil
			case "gh":
				if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
					// Simulate gh output containing a token line to guarantee strict redaction.
					return []byte("github.com\n  ✓ Logged in to github.com account example (keyring)\n  - Token: gho_SecretTokenThatMustNeverAppear123456\n"), nil
				}
				return []byte("gh version 2.67.0 (2025-02-11)\n"), nil
			case "docker":
				if len(args) >= 1 && args[0] == "version" {
					return []byte("27.5.1\n"), nil
				}
				return []byte("Docker version 27.5.1, build 9f9e405\n"), nil
			case "pg_dump":
				return []byte("pg_dump (PostgreSQL) 17.2\n"), nil
			case "psql":
				return []byte("psql (PostgreSQL) 17.2\n"), nil
			case "claude":
				return []byte("2.1.45 (Claude Code)\n"), nil
			case "codex":
				return []byte("codex-cli 0.118.0\n"), nil
			case "agy":
				return []byte("agy 0.9.3\n"), nil
			case "nvidia-smi":
				return []byte("NVIDIA GeForce RTX 4090, 24564, 560.35.03\n"), nil
			default:
				return nil, errors.New("unexpected command: " + base)
			}
		},
	}

	raw, err := n.handle(context.Background(), proto.MethodMachineToolchain, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handle(%s): %v", proto.MethodMachineToolchain, err)
	}
	rep, ok := raw.(proto.MachineToolchainReport)
	if !ok {
		t.Fatalf("unexpected result type %T", raw)
	}

	// Verify core tools and agent CLIs.
	if rep.Cached {
		t.Error("first call should not be cached")
	}
	if rep.CacheTTLSec != 60 {
		t.Errorf("cache_ttl_sec = %d, want 60", rep.CacheTTLSec)
	}

	assertTool := func(list []proto.ToolchainEntry, id, wantVer, wantPath, wantStatus string) proto.ToolchainEntry {
		t.Helper()
		for _, e := range list {
			if e.ID == id {
				if !e.Found && wantStatus != "missing" {
					t.Errorf("%s: Found=false, want true", id)
				}
				if e.Version != wantVer {
					t.Errorf("%s: Version=%q, want %q", id, e.Version, wantVer)
				}
				if wantPath != "" && e.Path != wantPath {
					t.Errorf("%s: Path=%q, want %q", id, e.Path, wantPath)
				}
				if e.Status != wantStatus {
					t.Errorf("%s: Status=%q, want %q", id, e.Status, wantStatus)
				}
				return e
			}
		}
		t.Fatalf("tool %q missing from report", id)
		return proto.ToolchainEntry{}
	}

	assertTool(rep.Tools, "git", "2.47.1", "/usr/bin/git", "ready")
	assertTool(rep.Tools, "go", "1.24.2", filepath.Join(customGoBin, "go"), "ready")
	assertTool(rep.Tools, "node", "22.14.0", filepath.Join(nvmNodeBin, "node"), "ready")
	assertTool(rep.Tools, "npm", "10.9.2", filepath.Join(nvmNodeBin, "npm"), "ready")
	assertTool(rep.Tools, "pnpm", "9.15.4", filepath.Join(nvmNodeBin, "pnpm"), "ready")
	assertTool(rep.Tools, "python3", "3.12.7", "/usr/bin/python3", "ready")
	ghEntry := assertTool(rep.Tools, "gh", "2.67.0", "/usr/local/bin/gh", "ready")
	if !slices.Contains(ghEntry.Capabilities, "authenticated") {
		t.Errorf("gh capabilities = %v, want authenticated", ghEntry.Capabilities)
	}
	dockerEntry := assertTool(rep.Tools, "docker", "27.5.1", "/usr/bin/docker", "ready")
	if !slices.Contains(dockerEntry.Capabilities, "daemon_accessible") {
		t.Errorf("docker capabilities = %v, want daemon_accessible", dockerEntry.Capabilities)
	}
	assertTool(rep.Tools, "pg_dump", "17.2", "/usr/bin/pg_dump", "ready")
	assertTool(rep.Tools, "psql", "17.2", "/usr/bin/psql", "ready")

	claudeEntry := assertTool(rep.AgentCLIs, "claude", "2.1.45", filepath.Join(home, ".local", "bin", "claude"), "ready")
	if claudeEntry.ResolvedPath != "/opt/claude/bin/claude-real" {
		t.Errorf("claude ResolvedPath = %q", claudeEntry.ResolvedPath)
	}
	if !slices.Contains(claudeEntry.Capabilities, "symlink") {
		t.Errorf("claude capabilities = %v, want symlink", claudeEntry.Capabilities)
	}
	assertTool(rep.AgentCLIs, "codex", "0.118.0", filepath.Join(nvmNodeBin, "codex"), "ready")
	assertTool(rep.AgentCLIs, "agy", "0.9.3", filepath.Join(home, ".local", "bin", "agy"), "ready")
	assertTool(rep.AgentCLIs, "omp", "", "", "missing")

	// Verify Docker and GH structured summaries.
	if rep.Docker == nil || !rep.Docker.SocketAccessible || rep.Docker.ServerVersion != "27.5.1" {
		t.Fatalf("unexpected Docker info: %+v", rep.Docker)
	}
	if rep.GitHubCLI == nil || !rep.GitHubCLI.Authenticated || !slices.Equal(rep.GitHubCLI.Hosts, []string{"github.com"}) {
		t.Fatalf("unexpected GitHubCLI info: %+v", rep.GitHubCLI)
	}

	// Verify Hardware & Power.
	if rep.Hardware == nil || len(rep.Hardware.GPUs) != 1 {
		t.Fatalf("unexpected Hardware info: %+v", rep.Hardware)
	}
	if rep.Hardware.GPUs[0].Name != "NVIDIA GeForce RTX 4090" || rep.Hardware.GPUs[0].VRAMMB != 24564 || rep.Hardware.GPUs[0].Driver != "560.35.03" {
		t.Errorf("unexpected GPU: %+v", rep.Hardware.GPUs[0])
	}
	if rep.Hardware.Power == nil || !rep.Hardware.Power.HasBattery || rep.Hardware.Power.BatteryPercent == nil || *rep.Hardware.Power.BatteryPercent != 84 || rep.Hardware.Power.Source != "ac" || rep.Hardware.Power.SleepState != "deep" {
		t.Errorf("unexpected Power: %+v", rep.Hardware.Power)
	}

	// Verify Environment summary and strict redaction across the entire serialized JSON payload.
	if rep.Environment.Shell != "zsh" || !rep.Environment.ProxyConfigured || !rep.Environment.HasCustomGoPath {
		t.Errorf("unexpected Environment: %+v", rep.Environment)
	}
	if !slices.Contains(rep.Environment.VersionManagers, "nvm") || !slices.Contains(rep.Environment.VersionManagers, "mise") {
		t.Errorf("version managers = %v, want nvm and mise", rep.Environment.VersionManagers)
	}

	blob, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	body := string(blob)
	for _, secret := range []string{
		"gho_SecretTokenThatMustNeverAppear123456",
		"ghp_1234567890abcdef1234567890abcdef",
		"sk-proj-secretvalue9876543210",
		"supersecretpass",
		"proxy.example.com",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("secret %q leaked into toolchain report JSON", secret)
		}
	}
}

func TestMachineToolchainCacheRefreshAndSingleFlight(t *testing.T) {
	var runs atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	var currentTime atomic.Int64
	base := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	currentTime.Store(base.UnixNano())

	n := &Node{home: "/home/dev"}
	n.toolchain.deps = &toolchainDeps{
		goos:   "linux",
		goarch: "amd64",
		home:   "/home/dev",
		getenv: func(k string) string {
			if k == "PATH" {
				return "/usr/bin"
			}
			return ""
		},
		now: func() time.Time { return time.Unix(0, currentTime.Load()).UTC() },
		stat: func(p string) (os.FileInfo, error) {
			return nil, os.ErrNotExist
		},
		readDir:  func(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist },
		readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		lookPathIn: func(name string, _ []string) string {
			if name == "git" {
				return "/usr/bin/git"
			}
			return ""
		},
		run: func(ctx context.Context, _ []string, name string, _ ...string) ([]byte, error) {
			if filepath.Base(name) == "git" {
				call := runs.Add(1)
				if call == 1 {
					started <- struct{}{}
					<-gate
				}
				return []byte("git version 2.45.0\n"), nil
			}
			return nil, errors.New("not found")
		},
	}

	// Two concurrent callers share a single in-flight probe.
	var wg sync.WaitGroup
	var rep1, rep2 proto.MachineToolchainReport
	wg.Add(2)
	go func() {
		defer wg.Done()
		out, err := n.machineToolchain(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Errorf("call 1: %v", err)
			return
		}
		rep1 = out.(proto.MachineToolchainReport)
	}()
	<-started
	go func() {
		defer wg.Done()
		out, err := n.machineToolchain(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Errorf("call 2: %v", err)
			return
		}
		rep2 = out.(proto.MachineToolchainReport)
	}()
	close(gate)
	wg.Wait()

	if got := runs.Load(); got != 1 {
		t.Fatalf("concurrent probes ran git %d times, want 1", got)
	}
	if rep1.Cached {
		t.Error("leader call should not be marked cached")
	}
	if !rep2.Cached {
		t.Error("follower call should be marked cached")
	}

	// Subsequent call within TTL uses cache.
	currentTime.Store(base.Add(30 * time.Second).UnixNano())
	out, err := n.machineToolchain(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	rep3 := out.(proto.MachineToolchainReport)
	if !rep3.Cached || runs.Load() != 1 {
		t.Fatalf("within TTL: cached=%v runs=%d, want true and 1", rep3.Cached, runs.Load())
	}

	// Manual refresh bypasses cache.
	out, err = n.machineToolchain(context.Background(), json.RawMessage(`{"refresh":true}`))
	if err != nil {
		t.Fatal(err)
	}
	rep4 := out.(proto.MachineToolchainReport)
	if rep4.Cached || runs.Load() != 2 {
		t.Fatalf("manual refresh: cached=%v runs=%d, want false and 2", rep4.Cached, runs.Load())
	}

	// After TTL expires, next call probes again.
	currentTime.Store(base.Add(30*time.Second + toolchainCacheTTL + time.Second).UnixNano())
	out, err = n.machineToolchain(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	rep5 := out.(proto.MachineToolchainReport)
	if rep5.Cached || runs.Load() != 3 {
		t.Fatalf("after TTL: cached=%v runs=%d, want false and 3", rep5.Cached, runs.Load())
	}
}

func TestMachineToolchainDockerPermissionDeniedAndGHUnauthenticated(t *testing.T) {
	n := &Node{home: "/home/dev"}
	n.toolchain.deps = &toolchainDeps{
		goos:   "linux",
		goarch: "arm64",
		home:   "/home/dev",
		getenv: func(k string) string {
			if k == "PATH" {
				return "/usr/bin"
			}
			return ""
		},
		now: func() time.Time { return time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC) },
		stat: func(p string) (os.FileInfo, error) {
			if p == "/var/run/docker.sock" {
				return fakeFileInfo{name: "docker.sock"}, nil
			}
			return nil, os.ErrNotExist
		},
		readDir:  func(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist },
		readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		lookPathIn: func(name string, _ []string) string {
			if name == "docker" || name == "gh" {
				return "/usr/bin/" + name
			}
			return ""
		},
		dialUnix: func(_ context.Context, _ string) error {
			return os.ErrPermission
		},
		run: func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
			switch filepath.Base(name) {
			case "docker":
				return []byte("Docker version 26.1.0, build abcdef\n"), nil
			case "gh":
				if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
					return []byte("You are not logged into any GitHub hosts. Run gh auth login to authenticate.\n"), errors.New("exit status 1")
				}
				return []byte("gh version 2.60.0\n"), nil
			default:
				return nil, errors.New("missing")
			}
		},
	}

	out, err := n.machineToolchain(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := out.(proto.MachineToolchainReport)

	if rep.Docker == nil || rep.Docker.SocketAccessible || rep.Docker.Status != "permission_denied" {
		t.Fatalf("docker = %+v, want permission_denied", rep.Docker)
	}
	if rep.GitHubCLI == nil || rep.GitHubCLI.Authenticated || rep.GitHubCLI.Status != "unauthenticated" {
		t.Fatalf("gh = %+v, want unauthenticated", rep.GitHubCLI)
	}
	if e := findEntryByID(rep.Tools, "docker"); e == nil || e.Status != "permission_denied" {
		t.Fatalf("docker entry = %+v", e)
	}
	if e := findEntryByID(rep.Tools, "gh"); e == nil || e.Status != "unauthenticated" {
		t.Fatalf("gh entry = %+v", e)
	}
}

func TestMachineToolchainDarwinAppleSiliconPowerAndRedactionHelper(t *testing.T) {
	n := &Node{home: "/Users/dev"}
	n.toolchain.deps = &toolchainDeps{
		goos:   "darwin",
		goarch: "arm64",
		home:   "/Users/dev",
		getenv: func(k string) string {
			if k == "PATH" {
				return "/usr/bin"
			}
			return ""
		},
		now:        func() time.Time { return time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC) },
		stat:       func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		readDir:    func(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist },
		readFile:   func(string) ([]byte, error) { return nil, os.ErrNotExist },
		lookPathIn: func(string, []string) string { return "" },
		run: func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
			if name == "sysctl" {
				return []byte("Apple M3 Max\n"), nil
			}
			if name == "pmset" && len(args) >= 2 && args[1] == "batt" {
				return []byte("Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1234567)\t62%; discharging; 5:20 remaining\n"), nil
			}
			if name == "pmset" && len(args) >= 2 && args[1] == "assertions" {
				return []byte("   PreventUserIdleSystemSleep 1\n"), nil
			}
			return nil, errors.New("unexpected")
		},
	}

	out, err := n.machineToolchain(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := out.(proto.MachineToolchainReport)

	if rep.Hardware == nil || rep.Hardware.Chip != "Apple M3 Max" {
		t.Fatalf("hardware = %+v", rep.Hardware)
	}
	if len(rep.Hardware.GPUs) != 1 || rep.Hardware.GPUs[0].Vendor != "apple" || !rep.Hardware.GPUs[0].Unified {
		t.Fatalf("gpus = %+v", rep.Hardware.GPUs)
	}
	if rep.Hardware.Power == nil || rep.Hardware.Power.BatteryPercent == nil || *rep.Hardware.Power.BatteryPercent != 62 || rep.Hardware.Power.Source != "battery" || rep.Hardware.Power.SleepState != "prevent_sleep" {
		t.Fatalf("power = %+v", rep.Hardware.Power)
	}
	if rep.Hardware.Power.Charging == nil || *rep.Hardware.Power.Charging {
		t.Fatalf("charging = %v, want false", rep.Hardware.Power.Charging)
	}

	// Direct redaction check.
	redacted := redactToolchainText("failed with token=abc123456789 and Bearer eyJhbGciOiJIUzI1NiJ9 and https://u:p@host.local/x")
	for _, leaked := range []string{"abc123456789", "eyJhbGciOiJIUzI1NiJ9", "u:p@"} {
		if strings.Contains(redacted, leaked) {
			t.Errorf("redactToolchainText leaked %q in %q", leaked, redacted)
		}
	}
}

func TestMachineToolchainRejectsMalformedParams(t *testing.T) {
	n := &Node{home: "/home/dev"}
	if _, err := n.handle(context.Background(), proto.MethodMachineToolchain, json.RawMessage(`{"refresh":"yes"}`)); err == nil {
		t.Fatal("expected error for malformed machine.toolchain params")
	}
}
