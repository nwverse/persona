package hub

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/update"
)

type developmentView struct {
	Configured     bool   `json:"configured"`
	Branch         string `json:"branch"`
	ResolvedBranch string `json:"resolved_branch,omitempty"`
	MappedMaster   bool   `json:"mapped_master"`
	CurrentCommit  string `json:"current_commit,omitempty"`
}

func (u *updater) developmentConfig() *update.DevelopmentConfig {
	if u.h.cfg == nil {
		return nil
	}
	return u.h.cfg.Update.Development
}

func (u *updater) developmentAvailable() bool {
	c := u.developmentConfig()
	return c != nil && c.Repository != "" && len(c.Backup.Quiesce) > 0 && len(c.Backup.Resume) > 0 && u.h.build.Channel != update.Docker && runtime.GOOS != "windows"
}

func (u *updater) developmentLocked() bool { return u.settings.Source == "web-dev" }

func (u *updater) adoptionUnresolved() bool {
	_, err := os.Lstat(update.DevelopmentAdoptionPath(u.dir))
	return !errors.Is(err, os.ErrNotExist)
}

func (u *updater) autoLocked() bool {
	if u.developmentLocked() {
		return u.settings.DevelopmentAuto
	}
	return u.settings.auto()
}

func (u *updater) selectedDevelopment() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.developmentLocked()
}

func (u *updater) candidateNewer(r *update.Published) bool {
	if r == nil {
		return false
	}
	if r.Development != nil {
		return r.Development.Commit != u.h.build.Commit
	}
	return u.h.build.Newer(r.Manifest.Build)
}

func (u *updater) developmentHold(m *update.Manifest) string {
	// A known clean installed commit must be an ancestor for automatic adoption.
	// Unknown/dirty builds can still be updated manually, like release updates.
	return u.h.build.Hold(m)
}

func (u *updater) runDevelopment(ctx context.Context, operation string, input map[string]any, result any) error {
	if err := u.devRun(ctx, u.developmentConfig(), filepath.Join(u.dir, "development"), operation, input, result); err != nil {
		return i18n.Err("hub-selfupdate.err_development_phase", i18n.Args{"phase": operation})
	}
	return nil
}

func (u *updater) checkDevelopment(ctx context.Context) (*update.Published, error) {
	if !u.developmentAvailable() {
		return nil, i18n.Err("hub-selfupdate.err_development_config")
	}
	if err := u.developmentTopology(); err != nil {
		return nil, err
	}
	var candidate update.DevelopmentCandidate
	err := u.runDevelopment(ctx, "resolve", nil, &candidate)
	u.mu.Lock()
	u.lastCheck = time.Now()
	u.mu.Unlock()
	if err != nil {
		return nil, err
	}
	number := update.DevelopmentNumber(u.h.build.Number)
	m := &update.Manifest{Version: fmt.Sprintf("web-dev-%s-%d", candidate.Commit[:12], number), Build: number,
		Commit: candidate.Commit, Released: time.Now()}
	r := &update.Published{Manifest: m, Development: &candidate}
	u.mu.Lock()
	// A repeated check of the same object retains the attempt identity (including
	// its bad-build marker), rather than creating a new build to bypass rollback.
	if u.release != nil && u.release.Development != nil && u.release.Development.Commit == candidate.Commit {
		r = u.release
	}
	for _, record := range u.settings.History {
		if record.Source == "web-dev" && record.Commit == candidate.Commit && record.Result == "rolled_back" {
			r.Manifest.Build = record.ToBuild
			r.Manifest.Version = record.ToVersion
		}
	}
	if !u.updateActiveLocked() {
		u.release, u.blocked = r, nil
	}
	u.mu.Unlock()
	return r, nil
}

func (u *updater) buildDevelopment(ctx context.Context, r *update.Published) error {
	u.set("building", "web/hub/node", "")
	var built struct {
		Artifacts string   `json:"artifacts"`
		Ancestors []string `json:"ancestors"`
	}
	err := u.runDevelopment(ctx, "build", map[string]any{
		"candidate": r.Development, "version": r.Manifest.Version, "build": r.Manifest.Build,
		"platform": runtime.GOOS + "-" + runtime.GOARCH, "release_url": u.h.updateURL()}, &built)
	if err != nil {
		return err
	}
	ancestors, err := update.EncodeAncestors(built.Ancestors)
	if err != nil {
		return err
	}
	m, err := update.BuildManifest(built.Artifacts, *r.Manifest)
	if err != nil {
		return err
	}
	m.Ancestors = ancestors
	r.Manifest = m
	r.Source = (&url.URL{Scheme: "file", Path: built.Artifacts}).String()
	return nil
}

func (u *updater) backupDevelopment(ctx context.Context, r *update.Published, target string) (*update.DevelopmentBackup, error) {
	if err := u.developmentTopology(); err != nil {
		return nil, err
	}
	// Immutable package pointers belong to the release publisher. Do not alter it
	// or silently leave development nodes on the previous release.
	if info, err := os.Lstat(identity.NodeBinaryDir()); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("node package directory is publisher-managed; configure a dedicated development installation")
	}
	u.set("backing_up", "", "")
	programs := map[string]string{"personad": target}
	for _, name := range update.ProgramNames {
		path := filepath.Join(filepath.Dir(target), name)
		if _, err := os.Stat(path); err == nil {
			programs[name] = path
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	var backup update.DevelopmentBackup
	err := u.runDevelopment(ctx, "backup", map[string]any{
		"runtime": u.h.cfg.dir, "candidate": r.Development, "hub_database": u.h.cfg.DatabaseURL,
		"from_version": u.h.version, "from_commit": u.h.build.Commit, "executables": programs}, &backup)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.settings.DevelopmentBackup = &backup
	err = u.saveLocked(ctx)
	u.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &backup, nil
}

// Only the built-in child manager is known to share this Hub's package directory
// and restart with it. Independent managers keep their original update channel.
func (u *updater) developmentTopology() error {
	if u.h.cfg == nil || !u.h.cfg.Assistant.StartsBuiltin() {
		return i18n.Err("hub-selfupdate.err_development_topology")
	}
	for _, mc := range u.h.knownEntries() {
		if u.h.entryOwner(mc.ID) != devicesOwner {
			return i18n.Err("hub-selfupdate.err_development_topology")
		}
	}
	return nil
}

func pendingSource(p update.Pending) string {
	if p.Development != nil {
		return "web-dev"
	}
	return "release"
}

func pendingCommit(p update.Pending) string {
	if p.Development != nil {
		return p.Development.Commit
	}
	return ""
}

// detachDevelopmentWeb makes the newly embedded Web authoritative. Keep a local
// override with the same transaction so rollback restores the old UI too.
func (u *updater) detachDevelopmentWeb(p *update.Pending) error {
	web := filepath.Join(u.h.cfg.dir, "web")
	if _, err := os.Lstat(web); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	previous := filepath.Join(u.dir, "development", fmt.Sprintf("web-previous-%d", p.ToBuild))
	if err := os.MkdirAll(filepath.Dir(previous), 0700); err != nil {
		return err
	}
	if _, err := os.Lstat(previous); !errors.Is(err, os.ErrNotExist) {
		return errors.New("previous Web recovery directory already exists")
	}
	if err := os.Rename(web, previous); err != nil {
		return err
	}
	p.Web, p.WebPrev = web, previous
	return nil
}

// Remove only the checkout built by this exact invocation, after the existing
// installer has copied its artifacts. The owned helper directory is not a Git checkout supplied by a user.
func (u *updater) removeDevelopmentBuild(r *update.Published) {
	if r.Source == "" {
		return
	}
	parsed, err := url.Parse(r.Source)
	if err != nil || parsed.Scheme != "file" {
		return
	}
	root := filepath.Dir(parsed.Path)
	if filepath.Dir(root) == filepath.Join(u.dir, "development") && strings.HasPrefix(filepath.Base(root), "build-") {
		_ = os.RemoveAll(root)
	}
}
