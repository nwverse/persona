package node

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func testStorageDeps(t *testing.T) (storageDeps, string, string) {
	t.Helper()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".persona")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	pct := 45.0
	deps := storageDeps{
		goos:    "linux",
		home:    home,
		dataDir: dataDir,
		getenv:  func(string) string { return "" },
		now:     func() time.Time { return now },
		mounts: func() ([]proto.MountStat, bool) {
			return []proto.MountStat{
				{Mount: "/", Device: "/dev/vda2", FSType: "ext4", UsedBytes: 50 << 30, AvailBytes: 50 << 30, TotalBytes: 100 << 30, UsedPercent: 50, InodesTotal: 1000, InodesUsed: 450, InodesFree: 550, InodesPercent: &pct},
			}, true
		},
		available: func(string) (int64, bool) { return 50 << 30, true },
		lookPath:  func(string) (string, error) { return "", errors.New("not found") },
		runCmd:    func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not called") },
		runningProcs: func(context.Context) (map[string]bool, error) {
			return map[string]bool{}, nil
		},
		activeLogFiles: func(context.Context, []string) map[string]bool {
			return map[string]bool{}
		},
	}
	return deps, home, dataDir
}

func writeStorageFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectMachineStorage(t *testing.T) {
	deps, _, _ := testStorageDeps(t)
	rep := collectMachineStorage(deps)
	if !rep.Supported || rep.OS != "linux" || len(rep.Mounts) != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if rep.Mounts[0].Mount != "/" || rep.Mounts[0].InodesPercent == nil || *rep.Mounts[0].InodesPercent != 45 {
		t.Fatalf("unexpected mount: %+v", rep.Mounts[0])
	}
}

func TestCollectMachineStorageHotspotsCoversCategories(t *testing.T) {
	deps, home, dataDir := testStorageDeps(t)
	writeStorageFile(t, filepath.Join(home, ".cache", "go-build", "a.obj"), 2048)
	writeStorageFile(t, filepath.Join(home, ".cache", "pip", "wheel.whl"), 1024)
	writeStorageFile(t, filepath.Join(home, "go", "pkg", "mod", "cache.zip"), 4096)
	writeStorageFile(t, filepath.Join(home, ".npm", "_cacache", "tar.tgz"), 1536)
	writeStorageFile(t, filepath.Join(dataDir, "logs", "node.log"), 512)
	writeStorageFile(t, filepath.Join(dataDir, "logs", "node.log.1"), 1024)
	writeStorageFile(t, filepath.Join(dataDir, "backups", "snap-1.tar"), 3072)

	// Add a stale broken worktree under ~/.herdr/worktrees/repo/old-branch
	wtDir := filepath.Join(home, ".herdr", "worktrees", "repo", "old-branch")
	writeStorageFile(t, filepath.Join(wtDir, "payload.bin"), 2560)
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: /nonexistent/repo/.git/worktrees/old-branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mock docker system df --format json
	deps.lookPath = func(bin string) (string, error) {
		if bin == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New("missing")
	}
	deps.runCmd = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && len(args) >= 2 && args[0] == "system" && args[1] == "df" {
			return []byte(strings.Join([]string{
				`{"Type":"Images","TotalCount":"3","Active":"1","Size":"2GB","Reclaimable":"1GB (50%)"}`,
				`{"Type":"Build Cache","TotalCount":"10","Active":"0","Size":"500MB","Reclaimable":"500MB"}`,
			}, "\n")), nil
		}
		return nil, errors.New("unexpected cmd")
	}

	rep := collectMachineStorageHotspots(context.Background(), deps, proto.StorageHotspotsParams{TimeoutMS: 5000, MaxDepth: 4})
	if rep.Partial {
		t.Fatalf("expected non-partial scan, got partial")
	}
	byID := map[string]proto.StorageHotspotCategory{}
	for _, c := range rep.Categories {
		byID[c.ID] = c
	}
	if byID[proto.StorageCategoryGoBuild].Bytes != 2048 || !byID[proto.StorageCategoryGoBuild].Cleanable {
		t.Errorf("go-build = %+v", byID[proto.StorageCategoryGoBuild])
	}
	if byID[proto.StorageCategoryGoMod].Bytes != 4096 || !byID[proto.StorageCategoryGoMod].Cleanable {
		t.Errorf("go-mod = %+v", byID[proto.StorageCategoryGoMod])
	}
	if byID[proto.StorageCategoryNpmCache].Bytes != 1536 {
		t.Errorf("npm-cache = %+v", byID[proto.StorageCategoryNpmCache])
	}
	if byID[proto.StorageCategoryPipCache].Bytes != 1024 {
		t.Errorf("pip-cache = %+v", byID[proto.StorageCategoryPipCache])
	}
	if byID[proto.StorageCategoryPersonaLogs].Bytes != 1536 || byID[proto.StorageCategoryPersonaLogs].ReclaimableBytes != 1024 || !byID[proto.StorageCategoryPersonaLogs].Cleanable {
		t.Errorf("persona-logs = %+v", byID[proto.StorageCategoryPersonaLogs])
	}
	if byID[proto.StorageCategoryPersonaBackups].Bytes != 3072 || !byID[proto.StorageCategoryPersonaBackups].Cleanable {
		t.Errorf("persona-backups = %+v", byID[proto.StorageCategoryPersonaBackups])
	}
	if byID[proto.StorageCategoryUserCache].Cleanable {
		t.Errorf("user-cache must be advisory (cleanable=false)")
	}
	if byID[proto.StorageCategoryDocker].Bytes != 2_500_000_000 || !byID[proto.StorageCategoryDocker].Cleanable || byID[proto.StorageCategoryDocker].CleanupTargetID != proto.StorageTargetDockerBuildCache {
		t.Errorf("docker = %+v", byID[proto.StorageCategoryDocker])
	}
	wt := byID[proto.StorageCategoryGitWorktrees]
	if wt.ItemCount != 1 || wt.CleanupTargetID != proto.StorageTargetGitWorktreesPrune || !wt.Items[0].Stale {
		t.Errorf("git-worktrees = %+v", wt)
	}
}

func TestMachineStorageCleanupValidationAndPreviewAndExecute(t *testing.T) {
	deps, home, dataDir := testStorageDeps(t)
	writeStorageFile(t, filepath.Join(home, ".cache", "pip", "a.whl"), 2048)
	activeLogPath := filepath.Join(dataDir, "logs", "node.log")
	rotatedLogPath := filepath.Join(dataDir, "logs", "node.log.1")
	writeStorageFile(t, activeLogPath, 512)
	writeStorageFile(t, rotatedLogPath, 1024)

	// Reject empty targets
	if _, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{DryRun: true}); err == nil {
		t.Fatal("expected error for empty targets")
	}
	// Reject arbitrary path / non-allowlisted target
	if _, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{Targets: []string{"/etc"}, Confirm: true}); err == nil {
		t.Fatal("expected error for unknown target")
	} else {
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != storageCodeBadRequest {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// Reject unconfirmed live cleanup
	if _, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{Targets: []string{proto.StorageTargetPipCache}}); err == nil {
		t.Fatal("expected confirmation_required error")
	} else {
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != storageCodeConfirmNeeded {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Dry-run preview must NOT delete files and must only count rotated logs (not active node.log)
	prev, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPipCache, proto.StorageTargetPersonaLogs},
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if !prev.DryRun || prev.EstimatedBytes != 3072 || prev.FreedBytes != 0 || len(prev.Items) != 2 {
		t.Fatalf("unexpected preview result: %+v", prev)
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "pip", "a.whl")); err != nil {
		t.Fatalf("dry run deleted pip cache file: %v", err)
	}
	if _, err := os.Stat(rotatedLogPath); err != nil {
		t.Fatalf("dry run deleted rotated log file: %v", err)
	}

	// Confirmed execution deletes pip cache & rotated log while keeping active node.log intact
	res, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPipCache, proto.StorageTargetPersonaLogs},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if res.DryRun || res.FreedBytes != 3072 {
		t.Fatalf("unexpected cleanup result: %+v", res)
	}
	for _, it := range res.Items {
		if it.Status != proto.StorageCleanupCleaned {
			t.Errorf("item %+v not cleaned", it)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "pip", "a.whl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pip file still exists after cleanup")
	}
	if st, err := os.Stat(filepath.Join(home, ".cache", "pip")); err != nil || !st.IsDir() {
		t.Fatalf("pip dir itself should be preserved: %v", err)
	}
	if _, err := os.Stat(rotatedLogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotated log file still exists after cleanup")
	}
	if st, err := os.Stat(activeLogPath); err != nil || st.Size() != 512 {
		t.Fatalf("active node.log must remain intact after cleanup: %v", err)
	}
}

func TestMachineStorageCleanupRefusesSymlinkEscapeAndRespectsInUse(t *testing.T) {
	deps, home, _ := testStorageDeps(t)
	outside := t.TempDir()
	writeStorageFile(t, filepath.Join(outside, "secret.txt"), 1024)
	if err := os.MkdirAll(filepath.Join(home, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".cache", "pip")); err != nil {
		t.Fatal(err)
	}
	writeStorageFile(t, filepath.Join(home, ".cache", "go-build", "build.o"), 1024)
	deps.runningProcs = func(context.Context) (map[string]bool, error) { return map[string]bool{"go": true}, nil }

	res, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPipCache, proto.StorageTargetGoBuild},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("cleanup returned err: %v", err)
	}
	if res.Items[0].Status != proto.StorageCleanupFailed {
		t.Errorf("symlink target status = %q, want failed", res.Items[0].Status)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("symlink escape removed outside file!")
	}
	if res.Items[1].Status != proto.StorageCleanupInUse {
		t.Errorf("in-use target status = %q, want in_use", res.Items[1].Status)
	}
}

func TestMachineStorageCleanupFailsClosedWhenProcessListUnavailable(t *testing.T) {
	deps, home, dataDir := testStorageDeps(t)
	goFile := filepath.Join(home, ".cache", "go-build", "build.o")
	npmFile := filepath.Join(home, ".npm", "_cacache", "tar.tgz")
	pipFile := filepath.Join(home, ".cache", "pip", "wheel.whl")
	backupFile := filepath.Join(dataDir, "backups", "old.tar")
	writeStorageFile(t, goFile, 2048)
	writeStorageFile(t, npmFile, 1024)
	writeStorageFile(t, pipFile, 512)
	writeStorageFile(t, backupFile, 4096)

	deps.runningProcs = func(context.Context) (map[string]bool, error) {
		return nil, errors.New("procfs unavailable")
	}

	// Hotspot scan must mark toolchain caches in_use when process state is unknown
	hotspots := collectMachineStorageHotspots(context.Background(), deps, proto.StorageHotspotsParams{TimeoutMS: 5000, MaxDepth: 4})
	for _, c := range hotspots.Categories {
		switch c.ID {
		case proto.StorageCategoryGoBuild, proto.StorageCategoryNpmCache, proto.StorageCategoryPipCache:
			if !c.InUse {
				t.Errorf("category %s should be marked in_use when process list is unavailable: %+v", c.ID, c)
			}
		case proto.StorageCategoryPersonaBackups:
			if c.InUse {
				t.Errorf("persona-backups should not depend on toolchain process list: %+v", c)
			}
		}
	}

	// Confirmed cleanup must refuse toolchain caches (in_use) and leave their files intact,
	// while non-toolchain persona-backups can still be cleaned.
	res, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{
			proto.StorageTargetGoBuild,
			proto.StorageTargetNpmCache,
			proto.StorageTargetPipCache,
			proto.StorageTargetPersonaBackups,
		},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("cleanup returned err: %v", err)
	}
	for i, wantID := range []string{proto.StorageTargetGoBuild, proto.StorageTargetNpmCache, proto.StorageTargetPipCache} {
		if res.Items[i].TargetID != wantID || res.Items[i].Status != proto.StorageCleanupInUse {
			t.Errorf("item[%d] = %+v, want %s with status in_use", i, res.Items[i], wantID)
		}
	}
	if res.Items[3].Status != proto.StorageCleanupCleaned {
		t.Errorf("persona-backups status = %q, want cleaned", res.Items[3].Status)
	}
	for _, keep := range []string{goFile, npmFile, pipFile} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("cache file %s was deleted despite unavailable process list: %v", keep, err)
		}
	}
	if _, err := os.Stat(backupFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup file should have been cleaned")
	}
}

func TestReadRunningProcNamesCrossPlatformAndUnsupportedFailClosed(t *testing.T) {
	// Darwin ps output parsing (full paths, login shell dash, mixed case Python)
	darwinRun := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "/bin/ps" {
			t.Fatalf("unexpected darwin cmd: %s %v", name, args)
		}
		return []byte("/opt/homebrew/bin/go\n-zsh\n/Library/Frameworks/Python.framework/Versions/3.12/Resources/Python.app/Contents/MacOS/Python\n/usr/local/bin/node\n"), nil
	}
	darwinProcs, err := readRunningProcNamesForOS(context.Background(), "darwin", darwinRun)
	if err != nil {
		t.Fatalf("darwin process enum failed: %v", err)
	}
	snap := storageProcSnapshot{names: darwinProcs, known: true}
	if inUse, unknown := storageToolchainInUse([]string{"go", "gopls"}, snap); !inUse || unknown {
		t.Errorf("expected go in use on darwin, got inUse=%v unknown=%v", inUse, unknown)
	}
	if inUse, unknown := storageToolchainInUse([]string{"pip", "python"}, snap); !inUse || unknown {
		t.Errorf("expected python in use on darwin, got inUse=%v unknown=%v", inUse, unknown)
	}
	if inUse, unknown := storageToolchainInUse([]string{"npm", "node"}, snap); !inUse || unknown {
		t.Errorf("expected node in use on darwin, got inUse=%v unknown=%v", inUse, unknown)
	}

	// Darwin ps failure must fail closed
	if _, err := readRunningProcNamesForOS(context.Background(), "darwin", func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("ps failed")
	}); err == nil {
		t.Fatal("expected error when darwin ps fails")
	}

	// Windows tasklist CSV parsing
	winRun := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name != "tasklist" {
			t.Fatalf("unexpected windows cmd: %s", name)
		}
		return []byte("\"go.exe\",\"1234\",\"Console\",\"1\",\"10,000 K\"\n\"python3.12.exe\",\"5678\",\"Console\",\"1\",\"20,000 K\"\n"), nil
	}
	winProcs, err := readRunningProcNamesForOS(context.Background(), "windows", winRun)
	if err != nil {
		t.Fatalf("windows process enum failed: %v", err)
	}
	winSnap := storageProcSnapshot{names: winProcs, known: true}
	if inUse, _ := storageToolchainInUse([]string{"go"}, winSnap); !inUse {
		t.Errorf("expected go in use on windows")
	}
	if inUse, _ := storageToolchainInUse([]string{"python"}, winSnap); !inUse {
		t.Errorf("expected python3.12 in use on windows")
	}
	if inUse, _ := storageToolchainInUse([]string{"npm", "node"}, winSnap); inUse {
		t.Errorf("expected node not in use on windows")
	}

	// Unsupported OS must return an error so cleanup fails closed
	if _, err := readRunningProcNamesForOS(context.Background(), "plan9", nil); err == nil {
		t.Fatal("expected unsupported OS to return an error")
	}
}

func TestMachineStorageCleanupPreservesActiveAndOpenPersonaLogs(t *testing.T) {
	deps, _, dataDir := testStorageDeps(t)
	logsDir := filepath.Join(dataDir, "logs")
	activeNodeLog := filepath.Join(logsDir, "node.log")
	activeHubLog := filepath.Join(logsDir, "hub.log")
	rotatedLog1 := filepath.Join(logsDir, "node.log.1")
	rotatedLogGz := filepath.Join(logsDir, "node.log.2.gz")
	openRotatedLog := filepath.Join(logsDir, "link.log.1")

	writeStorageFile(t, activeNodeLog, 512)
	writeStorageFile(t, activeHubLog, 256)
	writeStorageFile(t, rotatedLog1, 1024)
	writeStorageFile(t, rotatedLogGz, 2048)
	writeStorageFile(t, openRotatedLog, 4096)

	realOpenRotated, err := filepath.EvalSymlinks(openRotatedLog)
	if err != nil {
		t.Fatal(err)
	}
	deps.activeLogFiles = func(context.Context, []string) map[string]bool {
		return map[string]bool{realOpenRotated: true}
	}

	// Confirmed cleanup deletes ONLY closed rotated logs; active *.log and open rotated log stay intact.
	res, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPersonaLogs},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Status != proto.StorageCleanupCleaned || res.Items[0].Bytes != 3072 {
		t.Fatalf("unexpected persona-logs cleanup result: %+v", res.Items)
	}
	for _, removed := range []string{rotatedLog1, rotatedLogGz} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected rotated log %s to be removed", removed)
		}
	}
	for _, kept := range []string{activeNodeLog, activeHubLog, openRotatedLog} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("expected active/open log %s to remain intact: %v", kept, err)
		}
	}

	// Second confirmed run with only active/open logs remaining must report in_use and delete nothing.
	res2, err := runMachineStorageCleanup(context.Background(), deps, proto.StorageCleanupParams{
		Targets: []string{proto.StorageTargetPersonaLogs},
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("second cleanup failed: %v", err)
	}
	if len(res2.Items) != 1 || res2.Items[0].Status != proto.StorageCleanupInUse || res2.Items[0].Bytes != 0 {
		t.Fatalf("expected in_use when only active/open logs remain, got: %+v", res2.Items)
	}
	for _, kept := range []string{activeNodeLog, activeHubLog, openRotatedLog} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("second cleanup removed protected log %s: %v", kept, err)
		}
	}

	// Real open file handle via node.logger + /proc/self/fd must also protect an open rotated log file.
	liveLogFile, err := os.OpenFile(filepath.Join(logsDir, "live.log.1"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer liveLogFile.Close()
	if _, err := liveLogFile.WriteString("live log line\n"); err != nil {
		t.Fatal(err)
	}
	n := &Node{home: filepath.Dir(dataDir), dir: dataDir, logger: log.New(liveLogFile, "", 0)}
	realLogsDir, err := filepath.EvalSymlinks(logsDir)
	if err != nil {
		t.Fatal(err)
	}
	active := n.collectActiveStorageLogFiles(context.Background(), runtime.GOOS, []string{realLogsDir})
	realLivePath, err := filepath.EvalSymlinks(liveLogFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !active[realLivePath] {
		t.Fatalf("expected open logger file %s in active set %v", realLivePath, active)
	}
}

func TestParseHumanBytes(t *testing.T) {
	cases := map[string]int64{
		"0B":            0,
		"512kB":         512_000,
		"12.5MB (100%)": 12_500_000,
		"2GB":           2_000_000_000,
		"1.5GiB (50%)":  1610612736,
		"bad":           0,
	}
	for in, want := range cases {
		if got := parseHumanBytes(in); got != want {
			t.Errorf("parseHumanBytes(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNodeHandleMachineStorageDispatch(t *testing.T) {
	n := &Node{home: t.TempDir()}
	out, err := n.handle(context.Background(), proto.MethodMachineStorage, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("machine.storage failed: %v", err)
	}
	if _, ok := out.(proto.MachineStorageReport); !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if _, err := n.handle(context.Background(), proto.MethodMachineStorageCleanup, json.RawMessage(`{"targets":["/"]}`)); err == nil {
		t.Fatal("expected bad_request for non-allowlisted target via handle")
	}
}
