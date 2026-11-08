package extension

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// Where a plugin comes from.
const (
	SourceBuiltin   = "builtin"   // compiled into personad; a row keeps its switch and settings
	SourceHubJSON   = "hub.json"  // hub.json's plugins[]: read-only here, never in the database
	SourceInstalled = "installed" // a package installed on the hub's machine, or a remote plugin
)

// SecretMost is the largest secret value kept.
const SecretMost = 16 << 10

// previewFor is how long a preview's staged package is kept.
const previewFor = 10 * time.Minute

// Record is a plugin's row (table plugins): what the user installed and chose.
type Record struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Origin string `json:"origin,omitempty"`
	// MarketKey is the verified official key captured from this installed package's
	// catalog entry, never inferred from the market's current mutable identity.
	MarketKey       string `json:"market_key,omitempty"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	// PreviousOrigin and PreviousMarketKey are PreviousVersion's own Origin and MarketKey, kept by the upgrade that
	// replaced it so a rollback restores that version's provenance, never the newer one's. An empty PreviousOrigin
	// (a row upgraded before they were kept) is no snapshot: a rollback then drops the verified key.
	PreviousOrigin    string          `json:"previous_origin,omitempty"`
	PreviousMarketKey string          `json:"previous_market_key,omitempty"`
	Manifest          json.RawMessage `json:"manifest"`
	Granted           Permissions     `json:"granted"`
	GrantedDigest     string          `json:"granted_digest,omitempty"`
	Enabled           bool            `json:"enabled"`
	Config            json.RawMessage `json:"config,omitempty"`
	Remote            *Remote         `json:"remote,omitempty"`
	// DisabledContributions are the contributions the user switched off ("<point>/<id>", docs/extensions/extensions.md "Contribution switches"):
	// they do not take effect while the rest of the extension runs. A connector's switch is the connector's own.
	DisabledContributions []string  `json:"disabled_contributions,omitempty"`
	InstalledAt           time.Time `json:"installed_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// Remote is a plugin that runs elsewhere and is reached on these sockets.
type Remote struct {
	RPCSocket  string `json:"rpc_socket,omitempty"`
	HTTPSocket string `json:"http_socket,omitempty"`
}

// Store keeps the registry (the hub's Postgres: tables plugins, plugin_secrets, plugin_audit).
type Store interface {
	Extensions(ctx context.Context) ([]Record, error)
	PutExtension(ctx context.Context, r Record) error
	// DeleteExtension deletes the row and its secrets.
	DeleteExtension(ctx context.Context, id string) error
	// ExtensionSecrets are the sealed secrets by name.
	ExtensionSecrets(ctx context.Context, id string) (map[string]string, error)
	PutExtensionSecret(ctx context.Context, id, name, sealed string) error
	DeleteExtensionSecret(ctx context.Context, id, name string) error
	AddExtensionAudit(ctx context.Context, id, action, actor string, detail json.RawMessage) error
}

// Sealer seals secrets with the manager's secret.key (the same as mail passwords and launchers' keys).
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// Extension is one registered extension.
type Extension struct {
	Record Record
	// Manifest is what takes effect: the manifest without the contributions the user switched off (Without). Full is
	// the manifest as installed: what the extension may do (permissions, the ids it holds) and every contribution the
	// user can switch.
	Manifest *Manifest
	Full     *Manifest
	Schema   *Schema
	// Dir is the package's directory ("" for a remote or hub.json plugin).
	Dir string
	// Problem is why it cannot run on this hub (engines, a missing package); it is then not started.
	Problem  string
	Warnings []string
	// problemErr retains the message for responses in the request's language; Problem stays the log/CLI text.
	problemErr error
	// Legacy is a hub.json plugin's entry, as written (its command is a path on the machine, not in a package).
	Legacy any
}

// ReadOnly: hub.json's, changed there only.
func (p *Extension) ReadOnly() bool { return p.Record.Source == SourceHubJSON }

// ProblemText is the compatibility problem in the client's language, when a translation is available.
func (p *Extension) ProblemText(l i18n.Lang) string {
	if p.problemErr != nil && p.problemErr.Error() == p.Problem {
		return i18n.ExtensionStatusText(l, i18n.Localize(p.problemErr, l))
	}
	return i18n.ExtensionStatusText(l, p.Problem)
}

// Change is what the registry tells the hub after it changed a plugin.
type Change struct {
	ID   string
	Kind string // installed | upgraded | rolled_back | removed | enabled | disabled | config | secret
	// Old is the plugin before the change (removed, upgraded, rolled back).
	Old *Extension
}

// Registry is every plugin the hub knows: built-in, from hub.json and installed.
type Registry struct {
	dir      string // <data dir>/plugins
	store    Store  // nil: nothing is kept (no database), nothing installs
	seal     Sealer
	build    int64
	platform string

	// OnChange is called after each change, outside the registry's lock.
	OnChange func(Change)
	// Logf is the hub's log (what Load did about an interrupted uninstall); nil: nothing is logged.
	Logf func(format string, args ...any)
	// RemoteManifest asks a remote plugin for its manifest (the hub's host; initialize, or GET /manifest).
	RemoteManifest func(ctx context.Context, r Remote) (*Manifest, error)
	// CanEnable says why a plugin (its source and manifest) may not be on now, nil when it may: asked before it is
	// switched on, installed (on at once) or upgraded while on. A rollback is not asked (it goes back to a version
	// that ran; the hub rolls back a broken upgrade itself).
	CanEnable func(ctx context.Context, source string, m *Manifest) error
	// PlanCandidates gives the main preview the joint upgrade graph; PlanGuard keeps market trust unchanged while committing.
	PlanCandidates func(context.Context, *Manifest, string) []*Manifest
	PlanGuard      func(context.Context, []*Preview) (func(), error)
	// BoundSecrets names the secrets of p that hold only for its configuration old, so changing it to next deletes
	// them (the built-in voice extension's key goes with its service's address, docs/extensions/extensions.md "Built-in voice extension").
	// Both are the configurations with the schema's defaults. SetConfig deletes them before it saves next, in the same
	// operation SetSecret runs in, so a secret never pairs with a configuration it was not set for. Nil: none.
	BoundSecrets func(p *Extension, old, next json.RawMessage) []string
	// SecretNeeds says why secret name of p may not be set under its configuration config (with the defaults), nil
	// when it may: SetSecret asks it in the same operation SetConfig runs in. Nil: any may.
	SecretNeeds func(p *Extension, name string, config json.RawMessage) error
	// SettingsCheck asks an extension that declares settings_check whether its settings and secrets as they would be
	// after a change work (#5906): an error saves nothing. Called outside the registry's operation, with the values in
	// plain text, never kept.
	SettingsCheck func(ctx context.Context, p *Extension, config json.RawMessage, secrets map[string]string) error

	operationMu sync.Mutex // serializes package/record changes, including plan recovery and cleanup
	planProblem error      // while a commit confirmation or package recovery is unresolved, no new writes; wait for a reload
	mu          sync.Mutex
	extensions  map[string]*Extension
	reserved    map[string]map[string]string // point → contribution id → who has it (the hub's own)
	previews    map[string]*Preview
	now         func() time.Time
	// yields: built-in plugins an installed one of the same id takes the place of (Yields); yielded: those put aside.
	yields  map[string]bool
	yielded map[string]*Extension
	// devSecrets are the secrets of the extensions being written (SourceDev), in memory only.
	devSecrets map[string]map[string]string
	// keptOff are built-in extensions held off whatever their rows say (KeepOff).
	keptOff map[string]*keptOff
}

// keptOff is a built-in held off (KeepOff): what switching it on answers, and its row's own switch. The hold is not
// the owner's choice, so it never reaches the database: a row written meanwhile keeps the switch it had (put).
type keptOff struct {
	why error
	row bool // the row's switch; the default AddBuiltin was given while there is no row
}

// NewRegistry is a registry for the hub of build, keeping packages under dir.
func NewRegistry(dir string, store Store, seal Sealer, build int64) *Registry {
	return &Registry{dir: dir, store: store, seal: seal, build: build, platform: runtime.GOOS + "-" + runtime.GOARCH,
		extensions: map[string]*Extension{}, reserved: map[string]map[string]string{}, previews: map[string]*Preview{}, now: time.Now,
		yields: map[string]bool{}, yielded: map[string]*Extension{}}
}

// Yields marks the built-in plugin id as one an installed plugin of the same id takes the place of, while it moves out
// of personad into a package (Telegram, docs/plans/plan-extensions.md): installing that plugin puts the built-in aside (the hub
// stops its program before the installed one starts), uninstalling it brings the built-in back.
func (r *Registry) Yields(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.yields[id] = true
}

// yielding says whether cur is a built-in an installed plugin may take the place of. Called with r.mu held.
func (r *Registry) yielding(cur *Extension) bool {
	return cur != nil && cur.Record.Source == SourceBuiltin && r.yields[cur.Record.ID]
}

// Dirs.
func (r *Registry) installedDir(id, version string) string {
	return filepath.Join(r.dir, "installed", id, version)
}

// DataDir is a plugin's own directory for its caches and data (deleted with it).
func (r *Registry) DataDir(id string) string { return filepath.Join(r.dir, "data", id) }

// HasStore says whether installs are possible.
func (r *Registry) HasStore() bool { return r.store != nil }

// Reserve marks contribution ids the hub has itself (its built-in connectors), so no plugin takes them.
func (r *Registry) Reserve(point string, ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reserved[point] == nil {
		r.reserved[point] = map[string]string{}
	}
	for _, id := range ids {
		r.reserved[point][id] = "the hub"
	}
}

// AddHubJSON registers a hub.json plugin: its manifest made by the hub from the entry (legacy is the entry).
func (r *Registry) AddHubJSON(m *Manifest, legacy any, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.extensions[m.ID] != nil {
		return fmt.Errorf("plugin %q: duplicate id", m.ID)
	}
	raw, _ := json.Marshal(m)
	r.extensions[m.ID] = &Extension{Record: Record{ID: m.ID, Source: SourceHubJSON, Version: m.Version, Manifest: raw,
		Granted: m.Permissions, GrantedDigest: Digest(m), Enabled: enabled}, Manifest: m, Full: m, Legacy: legacy}
	return nil
}

// AddBuiltin registers a plugin compiled into personad (its program at dir, if any). Its switch and settings come
// from its row once Load reads it; enabled is the default.
func (r *Registry) AddBuiltin(m *Manifest, dir string, enabled bool) error {
	schema, err := ParseSchema(m.Configuration)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.extensions[m.ID] != nil {
		return fmt.Errorf("plugin %q: duplicate id", m.ID)
	}
	raw, _ := json.Marshal(m)
	r.extensions[m.ID] = &Extension{Record: Record{ID: m.ID, Source: SourceBuiltin, Version: m.Version, Manifest: raw,
		Granted: m.Permissions, GrantedDigest: Digest(m), Enabled: enabled}, Manifest: m, Full: m, Schema: schema, Dir: dir}
	return nil
}

// KeepOff holds the built-in extension id (registered, before Load) switched off whatever its row says; switching it
// on answers why. For a built-in that took over a hub.json entry written disabled (docs/extensions/extensions.md "Built-in voice extension"):
// hub.json has the last word on it, its settings and secrets stay editable, and its row keeps its own switch for when
// hub.json lets it run.
func (r *Registry) KeepOff(id string, why error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.extensions[id]
	if p == nil || p.Record.Source != SourceBuiltin {
		return
	}
	if r.keptOff == nil {
		r.keptOff = map[string]*keptOff{}
	}
	r.keptOff[id] = &keptOff{why: why, row: p.Record.Enabled}
	p.Record.Enabled = false
}

// Load reads the rows: installed plugins, and the switches and settings of built-in ones. A row whose package is gone
// or that this hub cannot run is kept and listed with its problem.
func (r *Registry) Load(ctx context.Context) error {
	unlock, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer unlock()
	if r.store == nil {
		return nil
	}
	rows, err := r.store.Extensions(ctx)
	if err != nil {
		return err
	}
	r.recoverUninstalls(rows) // before the rows are read: a package put back is found where its row says
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropStalePreviews()
	for _, row := range rows {
		cur := r.extensions[row.ID]
		switch {
		case row.Source == SourceBuiltin && cur != nil && cur.Record.Source == SourceBuiltin:
			cur.Record.Enabled, cur.Record.Config = row.Enabled, row.Config
			if k := r.keptOff[row.ID]; k != nil {
				k.row, cur.Record.Enabled = row.Enabled, false
			}
			cur.Record.DisabledContributions = row.DisabledContributions
			cur.Manifest = cur.Full.Without(row.DisabledContributions)
			cur.Record.InstalledAt, cur.Record.UpdatedAt = row.InstalledAt, row.UpdatedAt
		case row.Source == SourceInstalled && cur == nil:
			r.extensions[row.ID] = r.fromRow(row)
		case row.Source == SourceInstalled && r.yielding(cur):
			r.yielded[row.ID] = cur
			r.extensions[row.ID] = r.fromRow(row)
		}
	}
	return nil
}

// fromRow is an installed plugin as its row says.
func (r *Registry) fromRow(row Record) *Extension {
	p := &Extension{Record: row}
	m, err := Parse(row.Manifest)
	if err != nil {
		var loose Manifest
		_ = json.Unmarshal(row.Manifest, &loose)
		loose.ID = row.ID
		p.Manifest, p.Full = &loose, &loose
		p.problemErr = i18n.Err("extension-status.manifest_unreadable", i18n.Args{"err": err.Error()})
		p.Problem = p.problemErr.Error()
		return p
	}
	p.Full, p.Manifest = m, m.Without(row.DisabledContributions)
	p.Schema, _ = ParseSchema(m.Configuration)
	if row.Remote == nil {
		p.Dir = r.installedDir(row.ID, row.Version)
		if !hasManifest(p.Dir) {
			p.problemErr = i18n.Err("extension-status.package_missing", i18n.Args{"path": p.Dir})
			p.Problem = p.problemErr.Error()
		}
	}
	if w, err := CheckEngines(m, r.build); err != nil {
		p.Problem = err.Error()
		p.problemErr = err
	} else if w != "" {
		p.Warnings = append(p.Warnings, w)
	}
	p.Warnings = append(p.Warnings, m.Lint()...)
	return p
}

// List is every plugin, by id.
func (r *Registry) List() []*Extension {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Extension, 0, len(r.extensions))
	for _, p := range r.extensions {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Record.ID < out[j].Record.ID })
	return out
}

// Get is the plugin with id, nil when there is none.
func (r *Registry) Get(id string) *Extension {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.extensions[id]
}

// Snapshot is an extension's stable state for one process specification. Parsed manifests, schemas and legacy
// configuration are immutable and shared; the mutable record and its slices are copied under the registry lock.
func (r *Registry) Snapshot(id string) *Extension {
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshotExtension(r.extensions[id])
}

func snapshotExtension(p *Extension) *Extension {
	if p == nil {
		return nil
	}
	out := *p
	out.Record.Manifest = slices.Clone(p.Record.Manifest)
	out.Record.Config = slices.Clone(p.Record.Config)
	out.Record.Granted.Network = slices.Clone(p.Record.Granted.Network)
	out.Record.Granted.Hub = slices.Clone(p.Record.Granted.Hub)
	out.Record.DisabledContributions = slices.Clone(p.Record.DisabledContributions)
	if p.Record.Remote != nil {
		remote := *p.Record.Remote
		out.Record.Remote = &remote
	}
	out.Warnings = slices.Clone(p.Warnings)
	return &out
}

// HubScopeAllowed checks the live permission record while holding the registry lock. Callers must not read the
// pointer returned by Get to authorize a request: SetEnabled can change its record concurrently.
func (r *Registry) HubScopeAllowed(id, scope string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.extensions[id]
	return p != nil && p.Record.Enabled && p.Problem == "" &&
		(p.Record.Granted.FullHub() || slices.Contains(p.Record.Granted.Hub, scope))
}

// HubScopeGranted is HubScopeAllowed without the legacy full-hub grant: the user granted scope itself. For what
// did not exist under that grant (models: a purpose of its own in Settings › Models).
func (r *Registry) HubScopeGranted(id, scope string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.extensions[id]
	return p != nil && p.Record.Enabled && p.Problem == "" && slices.Contains(p.Record.Granted.Hub, scope)
}

// SendsAsUser says whether extension id, enabled and compatible, has a connector contribution with this id that
// declares sends_as_user (ConnectorItem.SendsAsUser). It reads the live record under the registry lock, like
// HubScopeAllowed, and the manifest as installed (Full: what the user agreed to), not the one without the contributions
// switched off: a connector's switch is the connector's own (SwitchedElsewhere, Settings › Connectors), which neither
// takes it out of the manifest nor stops a send that already happened from being recorded.
func (r *Registry) SendsAsUser(id, contribution string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.extensions[id]
	if p == nil || !p.Record.Enabled || p.Problem != "" || p.Full == nil {
		return false
	}
	return slices.ContainsFunc(p.Full.SendsAsUser(), func(c ConnectorItem) bool { return c.ID == contribution })
}

// EventSubscribers snapshots the enabled, compatible extensions subscribed to topic. A nonempty scope additionally
// requires that actual grant (or the legacy full-hub grant); an empty scope preserves the other event topics' rules.
func (r *Registry) EventSubscribers(topic, scope string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := []string{}
	for id, p := range r.extensions {
		if !p.Record.Enabled || p.Problem != "" || p.Manifest == nil {
			continue
		}
		if scope != "" && !p.Record.Granted.FullHub() && !slices.Contains(p.Record.Granted.Hub, scope) {
			continue
		}
		if slices.Contains(p.Manifest.Topics(), topic) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// ErrorReportTaker is an extension given error reports: its error_reports contributions that take source.
type ErrorReportTaker struct {
	Extension     string
	Contributions []string
}

// ErrorReportTakers snapshots the enabled, compatible extensions granted error_reports whose contributions (the ones
// switched on) take reports from source; source "" asks whether any takes some.
func (r *Registry) ErrorReportTakers(source string) []ErrorReportTaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ErrorReportTaker
	for id, p := range r.extensions {
		if !p.Record.Enabled || p.Problem != "" || p.Manifest == nil || !p.Record.Granted.ErrorReports {
			continue
		}
		t := ErrorReportTaker{Extension: id}
		for _, it := range p.Manifest.ErrorReports() {
			if source == "" || len(it.Sources) == 0 || slices.Contains(it.Sources, source) {
				t.Contributions = append(t.Contributions, it.ID)
			}
		}
		if len(t.Contributions) > 0 {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Extension < out[j].Extension })
	return out
}

// Preview is a staged package (or a remote plugin) and what installing it means, shown before the user agrees.
type Preview struct {
	ID          string           `json:"preview_id"`
	Manifest    *Manifest        `json:"manifest"`
	Permissions []PermissionLine `json:"permissions"`
	Digest      string           `json:"permissions_digest"`
	Warnings    []string         `json:"warnings,omitempty"`
	ExpiresAt   time.Time        `json:"expires_at"`
	// Upgrade: of the installed plugin with this id, from version From; MorePermissions: it asks for more than the
	// user granted, so the upgrade needs the digest.
	Upgrade         bool   `json:"upgrade,omitempty"`
	From            string `json:"from,omitempty"`
	MorePermissions bool   `json:"more_permissions,omitempty"`
	// AddedPermissions are the lines of what the new version may do that the installed one may not (only what is
	// new: the added hosts, the added secrets), for the upgrade screen.
	AddedPermissions []PermissionLine `json:"added_permissions,omitempty"`
	// Market is the market it comes from (a full-rights device may install it, docs/extensions/extensions.md "Extension market").
	Market string `json:"market,omitempty"`
	// Depends are its dependencies on this hub (docs/extensions/extensions.md "Dependencies"); Bundle the previews of what installs with it,
	// before it: its missing dependencies from the markets, or a pack's members. Each has its own id and digest, and
	// installing sends them all (the user agreed to every one on one screen).
	Depends []DependencyState `json:"depends,omitempty"`
	Bundle  []*Preview        `json:"bundle,omitempty"`
	// Unresolved are the dependencies or members no registered market has (it does not install while one is missing).
	Unresolved []string `json:"unresolved,omitempty"`

	previous              *Manifest // immutable consent baseline captured when the preview was staged
	source                Source
	marketSpec, marketKey string
	verifiedMarketKey     string // verified entry and package, separate from the mutable market's pin
	fingerprint           string // all staged package bytes/modes as approved at preview
	root                  string // the staged package
	staging               string
}

// MarketTrust is the market address and trusted public key pinned at preview time; it adds no protocol field.
func (pv *Preview) MarketTrust() (string, string) { return pv.marketSpec, pv.marketKey }

// previewIDPattern is what newID makes: a preview's staging directory is named so (staging/<id>).
var previewIDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

// dropStalePreviews removes the staging directories of previews an earlier run of the hub made and never installed
// or discarded: previews live in memory only, so after a restart nothing else would remove them. Only those older
// than previewFor go (expired wherever they were made), so a preview being staged now, or one of another hub still
// running on the same directory, stays. Other names in staging/ (plan-*, …) are left alone. Called with r.mu held.
func (r *Registry) dropStalePreviews() {
	root := filepath.Join(r.dir, "staging")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || !previewIDPattern.MatchString(e.Name()) || r.previews[e.Name()] != nil {
			continue
		}
		if fi, err := e.Info(); err == nil && r.now().Sub(fi.ModTime()) > previewFor {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// newID makes preview ids (and suffixes such as plan-<id>). A bare id directly under staging/ is a preview's
// directory and is removed on Load once expired (dropStalePreviews): other staging directories need a prefix.
func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Preview stages a package (or asks a remote plugin) and checks it for installing (upgrade "" ) or for upgrading
// the installed plugin upgrade.
func (r *Registry) Preview(ctx context.Context, source, upgrade string) (*Preview, error) {
	if r.store == nil {
		return nil, i18n.Err("extension-status.install_no_database")
	}
	src, err := ParseSource(source)
	if err != nil {
		return nil, err
	}
	return r.preview(ctx, src, upgrade, func(dir string) (string, error) {
		if src.Kind == "https" {
			reportState(ctx, StepDownload, StepRunning)
		} else {
			reportState(ctx, StepDownload, StepSkipped)
			reportState(ctx, StepVerify, StepSkipped)
		}
		return Stage(ctx, src, dir)
	})
}

// PreviewMarket is Preview for a plugin of a market: its package for this hub's platform, downloaded and checked
// against the index's sha256, must be the plugin the index says (id, version, permissions).
func (r *Registry) PreviewMarket(ctx context.Context, mk *Market, marketID string, e *MarketEntry, upgrade string, candidates ...*Manifest) (*Preview, error) {
	if r.store == nil {
		return nil, i18n.Err("extension-status.install_no_database")
	}
	verifiedKey := ""
	if IsOfficialMarket(mk.Spec.String()) {
		if e.verifiedKey == "" || e.verifiedKey != Official.SigningKey || e.verifiedKey != mk.TrustedKey || e.verifiedEntry != entryFingerprint(e) {
			return nil, i18n.Err("extension-market_security.signature")
		}
		verifiedKey = e.verifiedKey
	}
	pkg := e.PackageFor(Platform())
	if pkg == nil {
		return nil, fmt.Errorf("%s 没有这台 hub（%s）能用的包", e.Parsed.ID, Platform())
	}
	src := Source{Kind: "market", Path: marketID + "/" + e.Parsed.ID}
	pv, err := r.preview(ctx, src, upgrade, func(dir string) (string, error) {
		file, name, err := mk.Download(ctx, pkg, dir)
		if err != nil {
			return "", err
		}
		out := filepath.Join(dir, "pkg")
		reportState(ctx, StepUnpack, StepRunning)
		if err := unpackAs(file, name, out); err != nil {
			return "", err
		}
		_ = os.Remove(file)
		return packageRoot(out)
	}, candidates...)
	if err != nil {
		return nil, err
	}
	if m := pv.Manifest; !ManifestsEqual(m, e.Parsed) {
		if pv, _ := r.take(pv.ID); pv != nil {
			r.dropStaging(pv)
		}
		return nil, fmt.Errorf("下载的包（%s %s）的完整清单和市场索引（%s %s）不一致", m.ID, m.Version, e.Parsed.ID, e.Parsed.Version)
	}
	pv.Market, pv.marketSpec, pv.marketKey = marketID, mk.Spec.String(), mk.TrustedKey
	pv.verifiedMarketKey = verifiedKey
	return pv, nil
}

// MorePermissions says whether m asks for more than the installed cur was granted (its upgrade needs the user's
// confirmation).
func MorePermissions(cur *Extension, m *Manifest) bool {
	was := cur.Full
	return !cur.Record.Granted.Covers(m.Permissions) || (m.Runtime != nil && was.Runtime == nil) || NodeAdds(was, m) ||
		len(NewTools(was, m)) > 0 ||
		slices.ContainsFunc(m.Topics(), func(t string) bool { return !slices.Contains(was.Topics(), t) }) ||
		slices.ContainsFunc(m.Surfaces(), func(t string) bool { return !slices.Contains(was.Surfaces(), t) }) ||
		len(newChannels(was, m)) > 0 || len(newSendsAsUser(was, m)) > 0 ||
		slices.ContainsFunc(m.ContributesTo(), func(t string) bool { return !slices.Contains(was.ContributesTo(), t) })
}

// Discard drops a preview that will not be installed.
func (r *Registry) Discard(previewID string) {
	if pv, _ := r.take(previewID); pv != nil {
		r.dropStaging(pv)
	}
}

// PreviewMarketOf is the market a preview's package comes from ("" when it does not, or the preview is gone).
func (r *Registry) PreviewMarketOf(previewID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pv := r.previews[previewID]; pv != nil {
		return pv.Market
	}
	return ""
}

// PreviewUpload is Preview for a package uploaded in the browser (the admin UI): body is the .tar.gz / .zip named
// name, written into the preview's staging directory (at most PackageMost bytes) and unpacked there.
func (r *Registry) PreviewUpload(ctx context.Context, name string, body io.Reader, upgrade string) (*Preview, error) {
	if r.store == nil {
		return nil, i18n.Err("extension-status.install_no_database")
	}
	name, err := UploadName(name)
	if err != nil {
		return nil, err
	}
	return r.preview(ctx, Source{Kind: "upload", Path: name}, upgrade, func(dir string) (string, error) {
		reportState(ctx, StepDownload, StepSkipped)
		reportState(ctx, StepVerify, StepSkipped)
		reportState(ctx, StepUnpack, StepRunning)
		return StageUpload(body, name, dir)
	})
}

// preview checks what stage put into a fresh staging directory (or what a remote plugin says of itself).
func (r *Registry) preview(ctx context.Context, src Source, upgrade string, stage func(dir string) (string, error), candidates ...*Manifest) (*Preview, error) {
	r.expire()
	pv := &Preview{ID: newID(), source: src, ExpiresAt: r.now().Add(previewFor)}
	var m *Manifest
	var err error
	if src.Kind == "remote" {
		for _, step := range []string{StepDownload, StepVerify, StepUnpack} {
			reportState(ctx, step, StepSkipped)
		}
		reportState(ctx, StepCheck, StepRunning)
		if r.RemoteManifest == nil {
			return nil, errors.New("this hub cannot reach remote plugins")
		}
		if m, err = r.RemoteManifest(ctx, Remote{RPCSocket: src.RPCSocket, HTTPSocket: src.HTTPSocket}); err != nil {
			return nil, fmt.Errorf("问不到远端扩展的清单：%w", err)
		}
		if err := m.Validate(); err != nil {
			return nil, err
		}
		if err := CheckTriageRules(m); err != nil {
			return nil, err
		}
	} else {
		pv.staging = filepath.Join(r.dir, "staging", pv.ID)
		root, err := stage(pv.staging)
		if err != nil {
			_ = os.RemoveAll(pv.staging)
			return nil, err
		}
		pv.root = root
		reportState(ctx, StepUnpack, StepDone)
		reportState(ctx, StepCheck, StepRunning)
		data, err := os.ReadFile(ManifestPath(root))
		if err == nil {
			m, err = Parse(data)
		}
		if err == nil {
			err = r.checkPackageContext(ctx, m, root)
		}
		if err == nil {
			err = CheckTriageRules(m)
		}
		if err != nil {
			_ = os.RemoveAll(pv.staging)
			return nil, err
		}
	}
	pv.Manifest, pv.Digest, pv.Permissions = m, Digest(m), Explain(m)
	pv.Warnings = m.Lint()
	pv.Depends = r.DependenciesContext(ctx, m)
	fail := func(err error) (*Preview, error) {
		if pv.staging != "" {
			_ = os.RemoveAll(pv.staging)
		}
		return nil, err
	}
	w, err := CheckEngines(m, r.build)
	if err != nil {
		return fail(err)
	}
	if w != "" {
		pv.Warnings = append(pv.Warnings, w)
	}
	if src.Kind != "remote" {
		pv.fingerprint, err = packageFingerprint(pv.root)
		if err != nil {
			return fail(err)
		}
	}
	var joint []*Manifest
	if len(candidates) == 0 && r.PlanCandidates != nil {
		joint = r.PlanCandidates(ctx, m, FromMarket(src.String()))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.extensions[m.ID]
	switch {
	case upgrade == "" && cur != nil && !r.yielding(cur):
		return fail(fmt.Errorf("已经有一个叫 %s 的扩展（%s）：要换新版本用升级", m.ID, cur.Record.Source))
	case upgrade != "" && upgrade != m.ID:
		return fail(fmt.Errorf("这个包是 %s，不是 %s", m.ID, upgrade))
	case upgrade != "" && (cur == nil || cur.Record.Source != SourceInstalled):
		return fail(i18n.Err("extension-status.upgrade_not_installed", i18n.Args{"id": upgrade}))
	case upgrade != "" && !NewerVersion(m.Version, cur.Record.Version):
		return fail(fmt.Errorf("%s 不比已装的 %s 新", m.Version, cur.Record.Version))
	}
	if len(candidates) == 0 && (r.conflicts(m) != nil || r.checkDependencyGraph(m) != nil) {
		candidates = joint
	}
	candidates = previewCandidates(m, candidates)
	if err := r.conflictsCandidates(m, candidates); err != nil {
		return fail(err)
	}
	if err := r.checkDependencyGraph(candidates...); err != nil {
		return fail(err)
	}
	if upgrade != "" {
		pv.Upgrade, pv.From = true, cur.Record.Version
		pv.MorePermissions = MorePermissions(cur, m)
		pv.previous = cur.Full
		pv.AddedPermissions = Added(cur.Full, m)
	}
	r.previews[pv.ID] = pv
	return pv, nil // the check is done when the hub has resolved its bundle too (it says so)
}

// checkPackage holds a staged package to what a manifest says of it: its program is in it for this platform.
func (r *Registry) checkPackage(m *Manifest, root string) error {
	return r.checkPackageContext(context.Background(), m, root)
}
func (r *Registry) checkPackageContext(ctx context.Context, m *Manifest, root string) error {
	return r.checkPackageFiles(ctx, m, root, true)
}
func (r *Registry) checkPackageFiles(ctx context.Context, m *Manifest, root string, syntax bool) error {
	for _, a := range m.AssistantParts() {
		for _, sk := range a.Skills {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(sk), "SKILL.md")); err != nil {
				return fmt.Errorf("assistant %s: %s/SKILL.md 不在包里", a.ID, sk)
			}
		}
		if a.Instructions != "" {
			if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(a.Instructions))); err != nil || !fi.Mode().IsRegular() {
				return fmt.Errorf("assistant %s: %s 不在包里", a.ID, a.Instructions)
			}
		}
		if a.Gate != "" {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(a.Gate)))
			if err != nil {
				return fmt.Errorf("assistant %s: %s 不在包里", a.ID, a.Gate)
			}
			g, err := ParseGate(raw)
			if err != nil {
				return fmt.Errorf("assistant %s: %s: %v", a.ID, a.Gate, err)
			}
			// The script is in one of its skills (the skills directory on the assistant's machine is where they go).
			if !slices.ContainsFunc(a.Skills, func(sk string) bool { return strings.HasPrefix(g.Script, path.Base(sk)+"/") }) {
				return fmt.Errorf("assistant %s: the gate's script %s is not in its skills", a.ID, g.Script)
			}
		}
	}
	for _, l := range m.Looks() {
		if err := CheckLookFiles(l, root); err != nil {
			return err
		}
	}
	if m.Node != nil {
		if !NodePrograms {
			return errors.New("这个版本的 hub 还不能在电脑上运行扩展的程序（node 段），先升级 hub")
		}
		// Every computer's program is in the package: the hub sends each machine the one for its platform.
		for _, platform := range slices.Sorted(maps.Keys(m.Node.Command.ByPlatform)) {
			if err := checkProgram(root, "node.command", m.Node.Command.ByPlatform[platform]); err != nil {
				return err
			}
		}
	}
	if m.Runtime == nil {
		return nil
	}
	if m.Runtime.IsDeno() {
		if !syntax {
			_, err := CheckSourceBundleIntegrity(root, m.Runtime.Source)
			return err
		}
		_, err := CheckSourceBundleContext(ctx, root, m.Runtime.Source)
		return err
	}
	cmd, ok := m.Runtime.Command.For(r.platform)
	if !ok {
		return fmt.Errorf("这个扩展没有 %s 的程序", r.platform)
	}
	if IsBuiltinCommand(cmd) {
		return errors.New("runtime.command: only a built-in plugin runs the hub's own binary")
	}
	return checkProgram(root, "runtime.command", cmd)
}

// NodePrograms says whether this hub runs plugins' programs on computers (docs/plans/plan-extensions.md "7"): off, a package
// with a node program does not install, rather than install and never run there (a test turns it off).
var NodePrograms = true

// checkProgram holds a program a manifest names (at: which field) to being an executable file inside the package.
func checkProgram(root, at, cmd string) error {
	p := filepath.Join(root, filepath.FromSlash(cmd))
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return fmt.Errorf("%s %s 不在包里", at, cmd)
	}
	rootResolved, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(rootResolved, resolved); err != nil || !inside(filepath.ToSlash(rel)) {
		return fmt.Errorf("%s %s 跳出了包", at, cmd)
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 {
		return fmt.Errorf("%s %s 不是可执行文件", at, cmd)
	}
	return nil
}

// conflicts says which contribution of m another plugin or the hub has already. Called with r.mu held.
func (r *Registry) conflicts(m *Manifest) error {
	return r.conflictsCandidates(m, nil)
}

func (r *Registry) conflictsCandidates(m *Manifest, candidates []*Manifest) error {
	owners := make(map[string]*Manifest, len(r.extensions)+len(candidates))
	for id, other := range r.extensions {
		owners[id] = other.Full
	}
	for _, candidate := range candidates {
		owners[candidate.ID] = candidate
	}
	var errs []error
	for point, ids := range m.Contributions() {
		for _, id := range ids {
			if who, ok := r.reserved[point][id]; ok {
				errs = append(errs, fmt.Errorf("%s 里的 %q 已经是 %s 的", point, id, who))
			}
			for otherID, other := range owners {
				if otherID != m.ID && slices.Contains(other.Items(point), id) {
					errs = append(errs, fmt.Errorf("%s 里的 %q 已经是扩展 %s 的", point, id, otherID))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// The manifest actually read from the package overrides the planned one with the same ID; the market's description
// must not mask what was downloaded.
func previewCandidates(m *Manifest, candidates []*Manifest) []*Manifest {
	out := make([]*Manifest, 0, len(candidates)+1)
	for _, candidate := range candidates {
		if candidate != nil && candidate.ID != m.ID {
			out = append(out, candidate)
		}
	}
	return append(out, m)
}

// expire drops previews past their time.
func (r *Registry) expire() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, pv := range r.previews {
		if r.now().After(pv.ExpiresAt) {
			if pv.staging != "" {
				_ = os.RemoveAll(pv.staging)
			}
			delete(r.previews, id)
		}
	}
}

// take is the preview previewID, removed from the waiting ones.
// canEnable asks CanEnable, when the hub set it.
func (r *Registry) canEnable(ctx context.Context, source string, m *Manifest) error {
	if r.CanEnable == nil {
		return nil
	}
	return r.CanEnable(ctx, source, m)
}

// peek is a preview, left in place (a refusal before installing does not use it up).
func (r *Registry) peek(previewID string) *Preview {
	r.expire()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.previews[previewID]
}

func (r *Registry) take(previewID string) (*Preview, error) {
	r.expire()
	r.mu.Lock()
	defer r.mu.Unlock()
	pv := r.previews[previewID]
	if pv == nil {
		return nil, errors.New("预览过期了，重新预览一次")
	}
	delete(r.previews, previewID)
	return pv, nil
}

// Install installs a previewed package, digest being the one the user was shown. It is on at once, so CanEnable is
// asked first (a refusal keeps the preview).
func (r *Registry) Install(ctx context.Context, previewID, digest, actor string) (*Extension, error) {
	p, err := r.applyPlan(ctx, "", previewID, digest, nil, actor, false)
	if errors.Is(err, errPlanDigest) {
		r.Discard(previewID)
	} // legacy single-package consent consumes the refused preview
	return p, err
}

func (r *Registry) dropStaging(pv *Preview) {
	if pv.staging != "" {
		_ = os.RemoveAll(pv.staging)
	}
}

// Upgrade installs a previewed newer version of id. The digest is needed when it asks for more than was granted. A
// plugin that is on stays on, so CanEnable is asked about the new version first (a refusal keeps the preview).
func (r *Registry) Upgrade(ctx context.Context, id, previewID, digest, actor string) (*Extension, error) {
	p, err := r.applyPlan(ctx, id, previewID, digest, nil, actor, false)
	if errors.Is(err, errPlanDigest) {
		r.Discard(previewID)
	}
	return p, err
}

// prune deletes the package directories of id other than keep.
func (r *Registry) prune(id string, keep ...string) {
	entries, _ := os.ReadDir(filepath.Join(r.dir, "installed", id))
	for _, e := range entries {
		if !slices.Contains(keep, e.Name()) {
			_ = os.RemoveAll(filepath.Join(r.dir, "installed", id, e.Name()))
		}
	}
	r.pruneTools(id, keep...)
}

// Rollback goes back to the version installed before the last upgrade.
func (r *Registry) Rollback(ctx context.Context, id, actor string) (*Extension, error) {
	unlock, err := r.beginOperation()
	if err != nil {
		return nil, err
	}
	defer unlock()
	old := r.Snapshot(id)
	if old == nil || old.Record.Source != SourceInstalled {
		return nil, fmt.Errorf("没有装过扩展 %s", id)
	}
	prev := old.Record.PreviousVersion
	if prev == "" || old.Record.Remote != nil {
		return nil, errors.New("没有可以回到的上一个版本")
	}
	data, err := os.ReadFile(ManifestPath(r.installedDir(id, prev)))
	if err != nil {
		return nil, fmt.Errorf("上一个版本 %s 的包不在了", prev)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	var p *Extension
	err = func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		cur := r.extensions[id]
		if cur == nil || cur.Record.Version != old.Record.Version || cur.Record.PreviousVersion != prev {
			return errors.New("扩展在回滚之前变了，请重试")
		}
		if err := r.conflicts(m); err != nil {
			return err
		}
		if err := r.checkDependencyGraph(m); err != nil {
			return err
		}
		row := snapshotExtension(cur).Record
		row.Version, row.PreviousVersion, row.UpdatedAt = prev, "", r.now()
		// The old version's own provenance, never the current one's: a verified key goes back only with a snapshot.
		row.MarketKey = ""
		if row.PreviousOrigin != "" {
			row.Origin, row.MarketKey = row.PreviousOrigin, row.PreviousMarketKey
		}
		row.PreviousOrigin, row.PreviousMarketKey = "", ""
		row.Manifest, _ = json.Marshal(m)
		row.Granted, row.GrantedDigest = m.Permissions, Digest(m)
		if err := r.store.PutExtension(ctx, row); err != nil {
			return err
		}
		p = r.fromRow(row)
		r.extensions[id] = p
		r.prune(id, prev)
		return nil
	}()
	if err != nil {
		return nil, err
	}
	unlock()
	r.audit(ctx, id, "rollback", actor, map[string]any{"from": old.Record.Version, "to": prev})
	r.changed(Change{ID: id, Kind: "rolled_back", Old: old})
	return p, nil
}

// Uninstall removes an installed plugin: its row and secrets, its packages and (unless keepData) its data.
func (r *Registry) Uninstall(ctx context.Context, id string, keepData bool, actor string) error {
	unlock, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer unlock()
	old := r.Get(id)
	if old == nil {
		return i18n.Err("extension-status.extension_missing", i18n.Args{"id": id})
	}
	if old.Record.Source == SourceDev {
		unlock()
		return r.RemoveDev(id)
	}
	if old.Record.Source == SourceBuiltin {
		return i18n.Err("extension-status.builtin_disable_only", i18n.Args{"id": id})
	}
	if old.Record.Source != SourceInstalled {
		return i18n.Err("extension-status.config_disable_only", i18n.Args{"id": id})
	}
	var trash string
	err = func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		cur := r.extensions[id]
		if cur == nil || cur.Record.Source != SourceInstalled {
			return fmt.Errorf("扩展 %s 在卸载之前变了，请重试", id)
		}
		old = snapshotExtension(cur)
		dirs := []string{filepath.Join("installed", id), filepath.Join("tools", id)}
		if !keepData {
			dirs = append(dirs, filepath.Join("data", id))
		}
		// Its directories move aside before the row goes: one that cannot move fails the uninstall with nothing
		// changed, rather than leaving data behind an uninstall that said it worked. Load finishes or undoes a move
		// a crash interrupted (recoverUninstalls).
		aside, err := r.moveAside(old.Record, dirs)
		if err != nil {
			return err
		}
		if err := r.store.DeleteExtension(ctx, id); err != nil {
			if rerr := r.moveBack(aside); rerr != nil {
				return putBackFailed(err, aside, rerr)
			}
			return err
		}
		// The row is gone: from here these files are only ever removed, never put back (a reinstall of the same id
		// must not get the data the owner asked to delete). The identity in uninstall.json covers a crash before this.
		if aside != "" {
			_ = os.WriteFile(filepath.Join(aside, uninstallCommitted), nil, 0o600)
		}
		trash = aside
		delete(r.extensions, id)
		back := r.yielded[id]
		if back != nil {
			delete(r.yielded, id)
			r.extensions[id] = back
		}
		return nil
	}()
	if err != nil {
		return err
	}
	// Still inside the operation: a Load meanwhile would find the moved-aside directory too.
	var left error
	if trash != "" {
		if err := os.RemoveAll(trash); err != nil {
			left = &LeftoverError{ID: id, Path: trash, Err: err}
		}
	}
	unlock()
	r.audit(ctx, id, "uninstall", actor, map[string]any{"version": old.Record.Version, "keep_data": keepData})
	r.changed(Change{ID: id, Kind: "removed", Old: old})
	return left
}

// LeftoverError is an uninstall that happened (the row is gone) whose files could not all be removed: the caller
// reports the extension uninstalled, with Path to delete by hand.
type LeftoverError struct {
	ID, Path string
	Err      error
}

func (e *LeftoverError) Error() string {
	return i18n.T(i18n.Default, "extension-uninstall.leftover", i18n.Args{"id": e.ID, "path": e.Path, "err": e.Err.Error()})
}

func (e *LeftoverError) Unwrap() error { return e.Err }

// putBackFailed is err (what failed the uninstall) when putting the moved directories back failed too (rerr): the
// files stay in aside, named so they can be found.
func putBackFailed(err error, aside string, rerr error) error {
	return fmt.Errorf("%w: %w", err, i18n.Err("extension-uninstall.put_back_failed", i18n.Args{"path": aside, "err": rerr.Error()}))
}

// uninstallManifest is what a moved-aside directory holds besides the directories: whose they are and where each
// came from (relative to the registry's directory), so Load can put them back or finish removing them.
const uninstallManifest = "uninstall.json"

// uninstallCommitted, in a moved-aside directory, says the extension's row was deleted: its files are to be removed.
const uninstallCommitted = "committed"

type asideMoves struct {
	ID string `json:"id"`
	// Version and InstalledAt (Unix seconds: the database keeps less than nanoseconds) say which install moved: only
	// a row of the same install gets the files back.
	Version     string `json:"version"`
	InstalledAt int64  `json:"installed_at"`
	Moves       []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"moves"`
}

// moveAside renames each existing dir (relative to r.dir) into one fresh staging/uninstall-<id>-<rand> directory
// (the same file system, so a rename), after writing its manifest there. It returns that directory ("" when no dir
// existed). On an error what moved is put back; when that fails too the directory is kept and named in the error.
func (r *Registry) moveAside(row Record, dirs []string) (string, error) {
	id := row.ID
	plan := asideMoves{ID: id, Version: row.Version, InstalledAt: row.InstalledAt.Unix()}
	for i, d := range dirs {
		if _, err := os.Lstat(filepath.Join(r.dir, d)); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		plan.Moves = append(plan.Moves, struct {
			From string `json:"from"`
			To   string `json:"to"`
		}{d, strconv.Itoa(i)})
	}
	if len(plan.Moves) == 0 {
		return "", nil
	}
	aside := filepath.Join(r.dir, "staging", "uninstall-"+id+"-"+newID())
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return "", err
	}
	b, _ := json.Marshal(plan)
	if err := os.WriteFile(filepath.Join(aside, uninstallManifest), b, 0o600); err != nil {
		_ = os.RemoveAll(aside) // nothing moved yet
		return "", err
	}
	for _, m := range plan.Moves {
		from := filepath.Join(r.dir, m.From)
		if err := os.Rename(from, filepath.Join(aside, m.To)); err != nil {
			moved := fmt.Errorf("%w: %w", i18n.Err("extension-uninstall.move_failed", i18n.Args{"path": from}), err)
			if rerr := r.moveBack(aside); rerr != nil {
				return "", putBackFailed(moved, aside, rerr)
			}
			return "", moved
		}
	}
	return aside, nil
}

// moveBack puts what moveAside moved into aside back where it came from, then removes aside. It keeps aside (and
// says so) when anything could not go back, so nothing is deleted that is not where it belongs.
func (r *Registry) moveBack(aside string) error {
	if aside == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(aside, uninstallManifest))
	if err != nil {
		return err
	}
	var plan asideMoves
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	var errs []error
	for i := len(plan.Moves) - 1; i >= 0; i-- {
		m := plan.Moves[i]
		to, from := filepath.Join(aside, m.To), filepath.Join(r.dir, m.From)
		if !ownDir(plan.ID, m.From) || m.To == "" || strings.ContainsAny(m.To, `/\.`) {
			errs = append(errs, i18n.Err("extension-uninstall.bad_path", i18n.Args{"path": m.From}))
			continue
		}
		if _, err := os.Lstat(to); errors.Is(err, os.ErrNotExist) {
			continue // never moved
		}
		if _, err := os.Lstat(from); err == nil {
			errs = append(errs, i18n.Err("extension-uninstall.taken", i18n.Args{"path": from}))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(to, from); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return os.RemoveAll(aside)
}

// ownDir says whether rel is one of the directories an uninstall of id moves: installed/<id>, data/<id>, tools/<id>.
func ownDir(id, rel string) bool {
	if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
		return false
	}
	for _, d := range []string{"installed", "data", "tools"} {
		if filepath.Clean(rel) == filepath.Join(d, id) {
			return true
		}
	}
	return false
}

// recoverUninstalls finishes the uninstalls a crash interrupted (staging/uninstall-*). The directories go back only
// when the uninstall had not deleted the row (no committed marker) and the row there now is the same install
// (version, installed-at); otherwise, a reinstall of the same id included, they are removed. Failures go to Logf
// and keep the directory for the next start.
func (r *Registry) recoverUninstalls(rows []Record) {
	root := filepath.Join(r.dir, "staging")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "uninstall-") {
			continue
		}
		aside := filepath.Join(root, e.Name())
		var plan asideMoves
		b, err := os.ReadFile(filepath.Join(aside, uninstallManifest))
		if err == nil {
			err = json.Unmarshal(b, &plan)
		}
		if err != nil || plan.ID == "" {
			r.logf("extensions: %s: an interrupted uninstall without its record (%v); left for you to check", aside, err)
			continue
		}
		_, cerr := os.Lstat(filepath.Join(aside, uninstallCommitted))
		committed := cerr == nil
		same := slices.ContainsFunc(rows, func(row Record) bool {
			return row.ID == plan.ID && row.Source == SourceInstalled && row.Version == plan.Version && row.InstalledAt.Unix() == plan.InstalledAt
		})
		if !committed && same {
			if err := r.moveBack(aside); err != nil {
				r.logf("extensions: %s: putting back the files of an interrupted uninstall: %v (they are in %s)", plan.ID, err, aside)
			} else {
				r.logf("extensions: %s: an uninstall was interrupted before it happened; its files are back", plan.ID)
			}
			continue
		}
		if err := os.RemoveAll(aside); err != nil {
			r.logf("extensions: %s: removing the files of an uninstalled extension: %v; delete %s by hand", plan.ID, err, aside)
		} else {
			r.logf("extensions: %s: removed the files an interrupted uninstall left", plan.ID)
		}
	}
}

func (r *Registry) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// editable is a plugin the user may switch and configure (not hub.json's).
func (r *Registry) editable(id string) (*Extension, error) {
	p := r.Get(id)
	if p == nil {
		return nil, i18n.Err("extension-status.extension_missing", i18n.Args{"id": id})
	}
	if p.ReadOnly() {
		return nil, i18n.Err("extension-status.read_only", i18n.Args{"id": id})
	}
	if p.Record.Source == SourceDev {
		return nil, i18n.Err("extension-status.dev_read_only", i18n.Args{"id": id})
	}
	return p, nil
}

// put keeps p's row (a built-in one gets its first row here). A built-in held off (KeepOff) is written with its
// row's own switch, not the hold.
func (r *Registry) put(ctx context.Context, row Record) error {
	r.mu.Lock()
	if k := r.keptOff[row.ID]; k != nil {
		row.Enabled = k.row
	}
	r.mu.Unlock()
	return r.write(ctx, row)
}

// write keeps row as it is.
func (r *Registry) write(ctx context.Context, row Record) error {
	if r.store == nil {
		return nil // no database: the choice lasts until the hub restarts
	}
	if row.InstalledAt.IsZero() {
		row.InstalledAt = r.now()
	}
	row.UpdatedAt = r.now()
	return r.store.PutExtension(ctx, row)
}

// SetEnabled switches a plugin on or off; on only when CanEnable lets it.
func (r *Registry) SetEnabled(ctx context.Context, id string, on bool, actor string) error {
	unlock, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer unlock()
	p, err := r.editable(id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	held := r.keptOff[id]
	r.mu.Unlock()
	if on && held != nil {
		return held.why
	}
	if on && !p.Record.Enabled {
		if err := r.canEnable(ctx, p.Record.Source, p.Manifest); err != nil {
			return err
		}
	}
	r.mu.Lock()
	row := p.Record
	row.Enabled = on
	r.mu.Unlock()
	if err := r.write(ctx, row); err != nil { // the owner's choice, held off or not
		return err
	}
	r.mu.Lock()
	p.Record.Enabled = on
	if held != nil {
		held.row = on
	}
	r.mu.Unlock()
	kind := map[bool]string{true: "enabled", false: "disabled"}[on]
	unlock()
	r.audit(ctx, id, kind, actor, nil)
	r.changed(Change{ID: id, Kind: kind})
	return nil
}

// SetConfig replaces a plugin's configuration, held to its schema first.
func (r *Registry) SetConfig(ctx context.Context, id string, raw json.RawMessage, actor string) error {
	_, err := r.SetConfigClearing(ctx, id, raw, actor)
	return err
}

// ErrSecretNotCleared: a secret the new configuration unbinds (BoundSecrets) could not be deleted, so the
// configuration was not saved.
var ErrSecretNotCleared = i18n.Err("extension-status.secret_not_cleared")

// SetConfigClearing is SetConfig saying which secrets the change deleted (BoundSecrets): those that had a value, or
// may have had (they could not be read). They go first, fail closed: one that cannot be deleted saves nothing
// (ErrSecretNotCleared); a configuration that then cannot be saved answers its error with the ones already gone.
func (r *Registry) SetConfigClearing(ctx context.Context, id string, raw json.RawMessage, actor string) (cleared []string, err error) {
	checked, err := r.checkSettings(ctx, id, raw, "", "")
	if err != nil {
		return nil, err
	}
	unlock, err := r.beginOperation()
	if err != nil {
		return nil, err
	}
	defer unlock()
	p, err := r.editable(id)
	if err != nil {
		return nil, err
	}
	if err := r.stillChecked(p, checked); err != nil {
		return nil, err
	}
	if err := p.Schema.Check(raw); err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	compact, _ := json.Marshal(v)
	cleared, err = r.clearBound(ctx, p, compact)
	if err == nil {
		r.mu.Lock()
		row := p.Record
		row.Config = compact
		r.mu.Unlock()
		err = r.put(ctx, row)
	}
	if err == nil {
		r.mu.Lock()
		p.Record.Config = compact
		r.mu.Unlock()
	}
	unlock()
	for _, name := range cleared {
		r.audit(ctx, id, "secret_delete", actor, map[string]any{"name": name, "why": "config"})
	}
	if err != nil {
		if len(cleared) > 0 { // the program runs again without them, on the configuration it had
			r.changed(Change{ID: id, Kind: "secret"})
		}
		return cleared, err
	}
	r.audit(ctx, id, "config", actor, nil) // not the values: they may be personal
	r.changed(Change{ID: id, Kind: "config"})
	return cleared, nil
}

// clearBound deletes the secrets of p that configuration next unbinds (BoundSecrets), whether or not they are
// known to have a value (a failed read must not keep one), and says which went. Called in an operation.
func (r *Registry) clearBound(ctx context.Context, p *Extension, next json.RawMessage) ([]string, error) {
	if r.BoundSecrets == nil || r.store == nil {
		return nil, nil
	}
	names := r.BoundSecrets(p, r.Config(p), p.Schema.Effective(next))
	if len(names) == 0 {
		return nil, nil
	}
	had, readErr := r.store.ExtensionSecrets(ctx, p.Record.ID) // only to say which went
	var cleared []string
	for _, name := range names {
		if err := r.store.DeleteExtensionSecret(ctx, p.Record.ID, name); err != nil {
			return cleared, fmt.Errorf("%w（%s：%v）", ErrSecretNotCleared, name, err)
		}
		if _, ok := had[name]; ok || readErr != nil {
			cleared = append(cleared, name)
		}
	}
	return cleared, nil
}

// Config is what the plugin gets: its configuration with the schema's defaults.
func (r *Registry) Config(p *Extension) json.RawMessage {
	r.mu.Lock()
	raw := p.Record.Config
	r.mu.Unlock()
	return p.Schema.Effective(raw)
}

func (r *Registry) secretSpec(p *Extension, name string) error {
	if !slices.ContainsFunc(p.Manifest.Secrets, func(s SecretSpec) bool { return s.Name == name }) {
		return i18n.Err("extension-status.secret_missing", i18n.Args{"id": p.Record.ID, "name": name})
	}
	return nil
}

// SetSecret keeps one of a plugin's declared secrets, sealed. The value never goes to a log or back to anyone.
func (r *Registry) SetSecret(ctx context.Context, id, name, value, actor string) error {
	checked, err := r.checkSettings(ctx, id, nil, name, value)
	if err != nil {
		return err
	}
	unlock, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer unlock()
	p, err := r.editable(id)
	if err != nil {
		return err
	}
	if err := r.secretSpec(p, name); err != nil {
		return err
	}
	if err := r.stillChecked(p, checked); err != nil {
		return err
	}
	if r.SecretNeeds != nil {
		if err := r.SecretNeeds(p, name, r.Config(p)); err != nil {
			return err
		}
	}
	if value == "" || len(value) > SecretMost {
		return i18n.Err("extension-status.secret_size", i18n.Args{"max": SecretMost >> 10})
	}
	if r.store == nil || r.seal == nil {
		return i18n.Err("extension-status.secret_no_database")
	}
	if err := r.ensureRow(ctx, p); err != nil {
		return err
	}
	sealed, err := r.seal.Seal(value)
	if err != nil {
		return err
	}
	if err := r.store.PutExtensionSecret(ctx, id, name, sealed); err != nil {
		return err
	}
	unlock()
	r.audit(ctx, id, "secret_set", actor, map[string]any{"name": name})
	r.changed(Change{ID: id, Kind: "secret"})
	return nil
}

// DeleteSecret forgets one of a plugin's secrets.
func (r *Registry) DeleteSecret(ctx context.Context, id, name, actor string) error {
	unlock, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer unlock()
	p, err := r.editable(id)
	if err != nil {
		return err
	}
	if err := r.secretSpec(p, name); err != nil {
		return err
	}
	if r.store == nil {
		return nil
	}
	if err := r.store.DeleteExtensionSecret(ctx, id, name); err != nil {
		return err
	}
	unlock()
	r.audit(ctx, id, "secret_delete", actor, map[string]any{"name": name})
	r.changed(Change{ID: id, Kind: "secret"})
	return nil
}

// ensureRow gives a built-in plugin its row before a secret refers to it.
func (r *Registry) ensureRow(ctx context.Context, p *Extension) error {
	if p.Record.Source != SourceBuiltin {
		return nil
	}
	rows, err := r.store.Extensions(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(rows, func(x Record) bool { return x.ID == p.Record.ID }) {
		return nil
	}
	r.mu.Lock()
	row := p.Record
	r.mu.Unlock()
	return r.put(ctx, row)
}

// Secrets are a plugin's secrets in plain text, for handing to it only.
func (r *Registry) Secrets(ctx context.Context, id string) (map[string]string, error) {
	r.mu.Lock()
	dev, isDev := r.devSecrets[id]
	r.mu.Unlock()
	if isDev {
		return maps.Clone(dev), nil
	}
	if r.store == nil || r.seal == nil {
		return nil, nil
	}
	sealed, err := r.store.ExtensionSecrets(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(sealed))
	for name, s := range sealed {
		plain, err := r.seal.Open(s)
		if err != nil {
			return nil, i18n.Err("extension-status.secret_decrypt", i18n.Args{"id": id, "name": name})
		}
		out[name] = plain
	}
	return out, nil
}

// SecretsSet says which declared secrets have a value.
func (r *Registry) SecretsSet(ctx context.Context, p *Extension) map[string]bool {
	out := map[string]bool{}
	if p.Record.Source == SourceDev {
		r.mu.Lock()
		for _, s := range p.Manifest.Secrets {
			_, out[s.Name] = r.devSecrets[p.Record.ID][s.Name]
		}
		r.mu.Unlock()
		return out
	}
	if r.store == nil || p.ReadOnly() {
		return out
	}
	sealed, err := r.store.ExtensionSecrets(ctx, p.Record.ID)
	if err != nil {
		return out
	}
	for _, s := range p.Manifest.Secrets {
		_, out[s.Name] = sealed[s.Name]
	}
	return out
}

// Missing are what a plugin needs before it can run: required secrets without a value, a configuration its schema
// does not take (after an upgrade changed it). Empty: ready.
func (r *Registry) Missing(ctx context.Context, p *Extension) []string {
	if p == nil {
		return nil
	}
	g := r.dependencyGraph(ctx)
	if g.nodes[p.Record.ID] == nil {
		g.nodes[p.Record.ID] = snapshotExtension(p)
	}
	return slices.Clone(g.problems(p.Record.ID))
}

// Audit records what the hub changed about extension id outside the registry (its sandbox).
func (r *Registry) Audit(ctx context.Context, id, action, actor string, detail any) {
	r.audit(ctx, id, action, actor, detail)
}

func (r *Registry) audit(ctx context.Context, id, action, actor string, detail any) {
	if r.store == nil {
		return
	}
	b, _ := json.Marshal(detail)
	if detail == nil {
		b = []byte(`{}`)
	}
	_ = r.store.AddExtensionAudit(ctx, id, action, actor, b)
}

func (r *Registry) changed(c Change) {
	if r.OnChange != nil {
		r.OnChange(c)
	}
}

// ErrSettingsMoved: the settings changed while a change of them was being checked (settings_check); nothing saved.
var ErrSettingsMoved = i18n.Err("extension-status.settings_moved")

// settingsChecked is what a change was checked against: the configuration the extension had then (nil: not checked).
type settingsChecked struct{ config json.RawMessage }

// checkSettings has extension id check its settings as a change would leave them (SettingsCheck): config replaced
// by next (nil: kept), or secret name set to value (name ""). Only for one that declares settings_check, and only with
// values that pass the schema and the secret's rules first (those errors are the registry's own, later).
func (r *Registry) checkSettings(ctx context.Context, id string, next json.RawMessage, name, value string) (*settingsChecked, error) {
	p := r.Get(id)
	if p == nil || !p.Manifest.SettingsCheck || r.SettingsCheck == nil {
		return nil, nil
	}
	now := r.Config(p)
	config := now
	if next != nil {
		if p.Schema.Check(next) != nil {
			return nil, nil
		}
		config = p.Schema.Effective(next)
	}
	if name != "" && (r.secretSpec(p, name) != nil || value == "" || len(value) > SecretMost) {
		return nil, nil
	}
	secrets, err := r.Secrets(ctx, id)
	if err != nil {
		return nil, err
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	if name != "" {
		secrets[name] = value
	}
	if err := r.SettingsCheck(ctx, p, config, secrets); err != nil {
		return nil, err
	}
	return &settingsChecked{config: now}, nil
}

// stillChecked refuses a change whose check (checkSettings) saw other settings than p has now: one saved meanwhile
// was not checked with this one. Called in the operation.
func (r *Registry) stillChecked(p *Extension, c *settingsChecked) error {
	if c == nil || string(r.Config(p)) == string(c.config) {
		return nil
	}
	return ErrSettingsMoved
}
