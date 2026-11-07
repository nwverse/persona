package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"io"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/channel"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// ManifestMost is the largest persona-plugin.json read.
const ManifestMost = 256 << 10

// SecretsMost is how many secrets a plugin may declare.
const SecretsMost = 32

var (
	idPattern     = lazyre.New(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	secretPattern = lazyre.New(`^[a-z][a-z0-9_]{0,31}$`)
	envPattern    = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)
	platformKey   = lazyre.New(`^[a-z0-9]+-[a-z0-9]+$`)
)

// ValidID says whether id is a plugin id: a-z0-9_-, at most 32, not starting with - or _ (those are the hub's).
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Manifest is persona-plugin.json (docs/plans/plan-extensions.md "3").
type Manifest struct {
	ManifestVersion int    `json:"manifest_version"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Description     string `json:"description,omitempty"`
	Publisher       string `json:"publisher,omitempty"`
	Homepage        string `json:"homepage,omitempty"`
	// Tags are what the market shows and searches (docs/extensions/extensions.md "Extension market"). Packages and indexes carry the
	// manifest without them (PackagedManifest): hubs before tags read manifests strictly and would refuse it.
	Tags             []string                   `json:"tags,omitempty"`
	Engines          Engines                    `json:"engines,omitzero"`
	Runtime          *Runtime                   `json:"runtime,omitempty"`
	Node             *NodeRuntime               `json:"node,omitempty"`
	ActivationEvents []string                   `json:"activation_events,omitempty"`
	Contributes      map[string]json.RawMessage `json:"contributes,omitempty"`
	// OffByDefault are contributions ("<point>/<id>") that start switched off: at install, and at an upgrade for the
	// ones the version before did not have (docs/extensions/extensions.md "Contribution switches"). A connector's or a bound channel's
	// switch is not the extension's, so they cannot be listed.
	OffByDefault []string `json:"off_by_default,omitempty"`
	// Depends, Exports and ExtensionPack are the extension model (docs/extensions/extensions.md "Dependencies, exports and packs", model.go).
	Depends       []Dependency    `json:"depends,omitempty"`
	Exports       *Exports        `json:"exports,omitempty"`
	ExtensionPack []PackMember    `json:"extension_pack,omitempty"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
	Secrets       []SecretSpec    `json:"secrets,omitempty"`
	// SettingsCheck: the program checks a change of its settings or secrets before the hub saves it (settings.check,
	// configured.go, #5906).
	SettingsCheck bool        `json:"settings_check,omitempty"`
	Permissions   Permissions `json:"permissions,omitzero"`
	// Tools are command-line programs the hub installs for it (tools.go); Auth says it signs in (auth.* methods).
	// Hubs before them refuse the manifest (strict), so an extension using them asks for a newer hub in engines.
	Tools []ToolSpec `json:"tools,omitempty"`
	Auth  *AuthSpec  `json:"auth,omitempty"`
}

// Engines are what the extension runs on.
type Engines struct {
	// Persona is a range of hub build numbers (">=202611050000 <202701010000").
	Persona string
	// ExtensionProtocol is "extension_protocol"; its old name "plugin_protocol" is read too (docs/extensions/extensions.md
	// "Naming and compatibility"). It is still written out as "plugin_protocol", so the manifests the hub stores stay readable to a hub
	// rolled back to a build before #1844.
	ExtensionProtocol int
}

func (e Engines) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Persona  string `json:"persona,omitempty"`
		Protocol int    `json:"plugin_protocol,omitempty"`
	}{e.Persona, e.ExtensionProtocol})
}

func (e *Engines) UnmarshalJSON(data []byte) error {
	var v struct {
		Persona  string `json:"persona,omitempty"`
		Protocol int    `json:"extension_protocol,omitempty"`
		Legacy   int    `json:"plugin_protocol,omitempty"`
	}
	if err := strict(data, &v); err != nil {
		return err
	}
	if v.Protocol != 0 && v.Legacy != 0 && v.Protocol != v.Legacy {
		return fmt.Errorf("engines: extension_protocol %d and plugin_protocol %d differ", v.Protocol, v.Legacy)
	}
	e.Persona, e.ExtensionProtocol = v.Persona, max(v.Protocol, v.Legacy)
	return nil
}

// Runtime is how the plugin's program runs; a plugin without one runs no code.
type Runtime struct {
	// Mode is process (the default) or deno, a prebuilt source bundle in an independently confined runtime.
	Mode    string            `json:"mode,omitempty"`
	Source  *SourceRuntime    `json:"source,omitempty"`
	Command Command           `json:"command,omitzero"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// RPC: "stdio" (the default) speaks the plugin protocol on stdin / stdout; "none" does not.
	RPC string `json:"rpc,omitempty"`
	// HTTP: the program serves HTTP on PERSONA_PLUGIN_SOCKET, and the hub relays /api/v1/plugins/<id>/… to it.
	HTTP bool `json:"http,omitempty"`
	// Resources are the most it may use when the hub runs it in a sandbox (docs/extensions/extensions.md "Sandbox"); the defaults
	// otherwise.
	Resources *Resources `json:"resources,omitempty"`
}

// Resources cap a sandboxed program: memory in MB and CPU in percent of one core.
type Resources struct {
	MemoryMB   int `json:"memory_mb,omitempty"`
	CPUPercent int `json:"cpu_percent,omitempty"`
}

// The bounds of Resources, and what a program that names none gets.
const (
	ResourcesMemoryMin, ResourcesMemoryMax, ResourcesMemoryDefault = 64, 8192, 1024
	ResourcesCPUMin, ResourcesCPUMax, ResourcesCPUDefault          = 10, 800, 100
)

// Limits are r with the defaults filled in.
func (r *Resources) Limits() Resources {
	out := Resources{MemoryMB: ResourcesMemoryDefault, CPUPercent: ResourcesCPUDefault}
	if r != nil && r.MemoryMB > 0 {
		out.MemoryMB = r.MemoryMB
	}
	if r != nil && r.CPUPercent > 0 {
		out.CPUPercent = r.CPUPercent
	}
	return out
}

// SpeaksRPC says whether the program speaks the plugin protocol.
func (r *Runtime) SpeaksRPC() bool { return r != nil && r.RPC != "none" }

// NodeRuntime is the plugin's program that runs on computers, not on the hub (docs/plans/plan-extensions.md "7"): on each
// machine the user switched on in the configuration field Machines, started and stopped by that machine's node.
type NodeRuntime struct {
	// Command is one per platform ("darwin-arm64": "node/bin/x"); its platforms are the computers it runs on.
	Command Command           `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Machines names the configuration field that picks the computers: an array of format machine, its x-os the
	// systems Command has programs for (its maxItems, when it may run on one only: iMessage's Mac).
	Machines string `json:"machines"`
	RPC      string `json:"rpc,omitempty"`
	HTTP     bool   `json:"http,omitempty"`
	// KeepRunning is what it does while the hub cannot reach its machine: "lease" (the default) stops half an hour
	// after the hub last renewed it; "always" goes on (one that has to keep receiving, as iMessage).
	KeepRunning string `json:"keep_running,omitempty"`
	// Secrets are the names (of Manifest.Secrets) its machines get; the others stay on the hub.
	Secrets     []string        `json:"secrets,omitempty"`
	Permissions NodePermissions `json:"permissions,omitzero"`
}

// NodeBuiltin is node.command of a program built into personad (cua, ego, imessage; node/builtin.go): there is no
// package, the node answers plugin.node.call itself. Only the hub's own built-in extensions have it; a package with it
// is refused.
const NodeBuiltin = "builtin"

// Builtin says whether this is a program built into personad (node.command "builtin").
func (r *NodeRuntime) Builtin() bool {
	return r != nil && r.Command.ByPlatform == nil && r.Command.Path == NodeBuiltin
}

// NodeProgram is the node section of a program the hub sends to computers as a package: nil without one, and nil for
// a built-in program (NodeBuiltin), which has no package, no lease, no permission lines and no consent of its own. Use
// it, not Node, wherever packages, states, the field's confirm or consent are concerned.
func (m *Manifest) NodeProgram() *NodeRuntime {
	if m == nil || m.Node.Builtin() {
		return nil
	}
	return m.Node
}

// NodePermissions are what the program on the computers asks for, shown before installing and before a machine is
// switched on. Like Permissions, shown and recorded; the node does not enforce them yet.
type NodePermissions struct {
	// Network are the hosts it reaches from the computers.
	Network []string `json:"network,omitempty"`
	// Files: it reads and writes files outside its own data directory.
	Files bool `json:"files,omitempty"`
	// MacOSPrivacy: it asks for macOS's privacy permissions (Full Disk Access, Accessibility…), granted on that Mac.
	MacOSPrivacy bool `json:"macos_privacy,omitempty"`
}

// Keep-running choices of a node program.
const (
	KeepLease  = "lease"
	KeepAlways = "always"
)

// nodeSystems are the systems a node program can be for.
var nodeSystems = []string{"darwin", "linux"}

// SpeaksRPC says whether the program speaks the plugin protocol.
func (r *NodeRuntime) SpeaksRPC() bool { return r != nil && r.RPC != "none" }

// Systems are the systems the program has a build for ("darwin", "linux"), sorted, once each.
func (r *NodeRuntime) Systems() []string {
	var out []string
	for platform := range r.Command.ByPlatform {
		if sys, _, ok := strings.Cut(platform, "-"); ok && !slices.Contains(out, sys) {
			out = append(out, sys)
		}
	}
	slices.Sort(out)
	return out
}

// Command is one path, or one per platform ("linux-amd64": "bin/x").
type Command struct {
	Path       string
	ByPlatform map[string]string
}

func (c *Command) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &c.Path)
	}
	return json.Unmarshal(b, &c.ByPlatform)
}

func (c Command) MarshalJSON() ([]byte, error) {
	if c.ByPlatform != nil {
		return json.Marshal(c.ByPlatform)
	}
	return json.Marshal(c.Path)
}

// For is the command on platform ("linux-amd64"); false when there is none for it.
func (c Command) For(platform string) (string, bool) {
	if c.ByPlatform == nil {
		return c.Path, c.Path != ""
	}
	p, ok := c.ByPlatform[platform]
	return p, ok && p != ""
}

// Platforms are the platforms it has a program for, sorted (none for a single path).
func (c Command) Platforms() []string {
	out := make([]string, 0, len(c.ByPlatform))
	for p, path := range c.ByPlatform {
		if path != "" {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// SecretSpec is a secret the plugin needs.
type SecretSpec struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// Hint "last4": once set, the settings show its last four characters (configured.go, #5906).
	Hint string `json:"hint,omitempty"`
}

// Activation events.
const (
	OnStartup = "on_startup"
	OnCall    = "on_call"
	OnHTTP    = "on_http"
)

// Builtin commands: the hub's own binary (a built-in plugin's).
var builtinCommands = []string{"personad", compat.DaemonBinary}

// IsBuiltinCommand says whether a runtime command names the hub's own binary.
func IsBuiltinCommand(c string) bool { return slices.Contains(builtinCommands, c) }

// Parse reads a manifest strictly (no unknown fields anywhere) and checks it (Validate).
func Parse(data []byte) (*Manifest, error) {
	if len(data) > ManifestMost {
		return nil, fmt.Errorf("%s: %d bytes, more than %d", ManifestFile, len(data), ManifestMost)
	}
	var m Manifest
	if err := strict(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// strict decodes one JSON value into v, refusing unknown fields and anything after it.
func strict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("something after the JSON value")
	}
	return nil
}

// checkTags holds tags to what a market entry may have: at most marketTagMost, each 1 to 24 characters.
func checkTags(tags []string) error {
	if len(tags) > marketTagMost {
		return fmt.Errorf("more than %d", marketTagMost)
	}
	for _, t := range tags {
		if t == "" || utf8.RuneCountInString(t) > 24 {
			return errors.New("a tag is empty or longer than 24 characters")
		}
	}
	return nil
}

// PackagedManifest is the manifest as a package and a market index carry it: data without tags, which the market
// entry carries instead. A hub from before tags reads manifests strictly (no unknown fields) and would refuse one
// with them; the same bytes go in as persona-extension.json and as LegacyManifestFile.
func PackagedManifest(data []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := strict(data, &fields); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if _, ok := fields["tags"]; !ok {
		return data, nil
	}
	delete(fields, "tags")
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Validate checks everything a manifest can be checked for on its own (docs/plans/plan-extensions.md "Fields"). What depends on
// the hub (the platform's command, engines, ids other plugins use) is the registry's.
func (m *Manifest) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if m.ManifestVersion != ManifestVersion {
		bad("manifest_version %d: this hub reads %d", m.ManifestVersion, ManifestVersion)
	}
	if !ValidID(m.ID) {
		bad("id %q: a-z0-9_-, at most 32, a letter or digit first", m.ID)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(m.Name)); n == 0 || n > 40 {
		bad("name: 1 to 40 characters")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		bad("version %q: %v", m.Version, err)
	}
	if m.Homepage != "" {
		if u, err := url.Parse(m.Homepage); err != nil || u.Scheme != "https" || u.Host == "" {
			bad("homepage: an https URL")
		}
	}
	if err := checkTags(m.Tags); err != nil {
		bad("tags: %v", err)
	}
	if m.Engines.Persona != "" {
		if _, err := ParseRange(m.Engines.Persona); err != nil {
			bad("engines.persona: %v", err)
		}
	}
	if (m.Runtime != nil || m.Node != nil) && m.Engines.ExtensionProtocol != Protocol {
		bad("engines.extension_protocol %d: this hub speaks %d", m.Engines.ExtensionProtocol, Protocol)
	}
	if m.Runtime != nil {
		errs = append(errs, m.Runtime.validate()...)
		if m.Runtime.IsDeno() && (m.Node != nil || len(m.Tools) != 0 || m.Permissions.AgentRuntimes || m.Permissions.FullHub()) {
			bad("runtime deno: node programs, command-line tools, agent runtimes and unrestricted hub access are not supported")
		}
	}
	if m.Node != nil {
		errs = append(errs, m.Node.validate(m)...)
	}
	for _, e := range m.ActivationEvents {
		if !slices.Contains([]string{OnStartup, OnCall, OnHTTP}, e) {
			bad("activation_events: %q is not on_startup, on_call or on_http", e)
		}
	}
	if m.Runtime != nil && !m.Runtime.SpeaksRPC() && !slices.Contains(m.ActivationEvents, OnStartup) {
		bad("activation_events: a program that does not speak the protocol (rpc none) runs on_startup")
	}
	if m.Runtime == nil && len(m.ActivationEvents) > 0 {
		bad("activation_events without a runtime")
	}
	if slices.Contains(m.ActivationEvents, OnHTTP) && (m.Runtime == nil || !m.Runtime.HTTP) {
		bad("activation_events: on_http without runtime.http")
	}
	if len(m.Contributes) == 0 && len(m.ExtensionPack) == 0 {
		bad("contributes: nothing")
	}
	errs = append(errs, m.validateModel()...)
	errs = append(errs, m.validateTools()...)
	for _, name := range sortedKeys(m.Contributes) {
		p, known := Points[name]
		if !known {
			continue // a newer hub's (Lint warns, it does not take effect), or another extension's (validateModel)
		}
		var items []json.RawMessage
		if err := json.Unmarshal(m.Contributes[name], &items); err != nil {
			bad("contributes.%s: a list", name)
			continue
		}
		if len(items) == 0 {
			bad("contributes.%s: empty", name)
		}
		seen := map[string]bool{}
		for _, raw := range items {
			id, err := p.Item(raw)
			if err != nil {
				bad("contributes.%s: %v", name, err)
				continue
			}
			if seen[id] {
				bad("contributes.%s: %q twice", name, id)
			}
			seen[id] = true
		}
		if p.NeedsRPC && !m.Runtime.SpeaksRPC() {
			bad("contributes.%s needs a runtime that speaks the protocol", name)
		}
		if p.NeedsHTTP && (m.Runtime == nil || !m.Runtime.HTTP) {
			bad("contributes.%s needs runtime.http", name)
		}
		if name == "quota" && !m.Permissions.LauncherKeys {
			bad("contributes.quota needs permissions.launcher_keys (the user agrees that it reads quota with launchers' keys)")
		}
		if name == "api" && !slices.Contains(m.Activation(), OnStartup) {
			bad("contributes.api: the app's API is there all the time: activation_events on_startup")
		}
		if name == "api" {
			for _, it := range m.APIs() {
				if len(it.Events) > 0 && !m.Runtime.SpeaksRPC() {
					bad("contributes.api: %s pushes events (api.broadcast) but the plugin does not speak the protocol", it.ID)
				}
			}
		}
		if name == "messaging" {
			if !slices.Contains(m.Activation(), OnStartup) {
				bad("contributes.messaging: the 消息 entry is there all the time and pushes changes: activation_events on_startup")
			}
			pages := map[string]bool{}
			for _, u := range m.UIs() {
				pages[u.ID] = u.Placement == "extension_page" || u.Placement == "plugin_page"
			}
			for _, it := range m.Messaging() {
				if it.SettingsUI != "" && !pages[it.SettingsUI] {
					bad("contributes.messaging: %s: settings_ui %q is not an extension_page of this extension", it.ID, it.SettingsUI)
				}
				for _, l := range it.Links {
					if ui, ok := strings.CutPrefix(l.Opens, "ui:"); ok && !pages[ui] {
						bad("contributes.messaging: %s: a link opens %q, not an extension_page of this extension", it.ID, ui)
					}
				}
			}
		}
		if name == "error_reports" && !m.Permissions.ErrorReports {
			bad("contributes.error_reports needs permissions.error_reports (the user agrees that it gets the error and crash reports)")
		}
		if name == "auth_relay" && !m.Permissions.AuthRelay {
			bad("contributes.auth_relay needs permissions.auth_relay (the user agrees that it completes the authorization links their Agents stop at)")
		}
		if name == "error_reports" && !slices.Contains(m.Activation(), OnStartup) {
			bad("contributes.error_reports: reports are given to a plugin that runs: activation_events on_startup")
		}
		if name == "events" && !slices.Contains(m.Activation(), OnStartup) {
			bad("contributes.events: events are told to a plugin that runs: activation_events on_startup")
		}
		if name == "agent_sources" && !slices.Contains(m.Activation(), OnStartup) {
			bad("contributes.agent_sources: the Agents tab lists it all the time: activation_events on_startup")
		}
		if name == "channel" {
			for _, c := range m.Channels() {
				switch {
				case c.Connector != "" && !slices.Contains(m.Items("connectors"), c.Connector):
					bad("contributes.channel: %s binds through connector %s, which the plugin does not contribute", c.ID, c.Connector)
				case c.Connector == "" && !slices.Contains(m.Activation(), OnStartup):
					bad("contributes.channel: %s has no connector to run with: activation_events on_startup", c.ID)
				}
			}
		}
		if name == "ui" {
			uis := m.UIs()
			for i, u := range uis {
				if u.Placement == "launcher_section" && !m.Permissions.LauncherKeys {
					bad("contributes.ui: %s sits on launchers' pages and reaches their gateways: permissions.launcher_keys", u.ID)
				}
				if u.Opens != "" && !slices.ContainsFunc(uis, func(o UIItem) bool {
					return o.ID == u.Opens && FullPage(o.Placement)
				}) {
					bad("contributes.ui: %s opens %q, which is not a nav_page or extension_page of this extension", u.ID, u.Opens)
				}
				if u.Placement != "inbox_item" || u.Connector == "" {
					continue
				}
				// A plugin draws only the items its own connector brought in: another source's (mail, GitHub…) are
				// not its to read, nor its to draw.
				if !slices.Contains(m.Items("connectors"), u.Connector) {
					bad("contributes.ui: %s draws the inbox items of connector %s, which the plugin does not contribute", u.ID, u.Connector)
				}
				if slices.ContainsFunc(uis[:i], func(o UIItem) bool { return o.Placement == "inbox_item" && o.Connector == u.Connector }) {
					bad("contributes.ui: more than one inbox_item draws connector %s's items", u.Connector)
				}
			}
		}
		if name == "shortcuts" {
			for _, s := range m.Shortcuts() {
				if s.UI != "" && !slices.ContainsFunc(m.UIs(), func(o UIItem) bool { return o.ID == s.UI && FullPage(o.Placement) }) {
					bad("contributes.shortcuts: %s opens %q, which is not a nav_page or extension_page of this extension", s.ID, s.UI)
				}
			}
		}
		if name == "providers" {
			for _, it := range m.Providers() {
				if it.DynamicModels && !m.Runtime.SpeaksRPC() {
					bad("contributes.providers: %s lists its models itself (dynamic_models) but the plugin runs no program that speaks the protocol", it.ID)
				}
			}
		}
	}
	if len(m.Configuration) > 0 {
		if _, err := ParseSchema(m.Configuration); err != nil {
			bad("configuration: %v", err)
		}
	}
	for _, msg := range m.agentFilterSwitches() {
		bad("%s", msg)
	}
	for _, msg := range m.tunnelTargetProblems() {
		bad("%s", msg)
	}
	for _, msg := range m.offByDefaultProblems() {
		bad("%s", msg)
	}
	if len(m.Secrets) > SecretsMost {
		bad("secrets: more than %d", SecretsMost)
	}
	seen := map[string]bool{}
	for _, s := range m.Secrets {
		if !secretPattern.MatchString(s.Name) {
			bad("secrets: name %q: a-z0-9_, a letter first, at most 32", s.Name)
		}
		if seen[s.Name] {
			bad("secrets: %q twice", s.Name)
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Title) == "" {
			bad("secrets: %q has no title", s.Name)
		}
	}
	errs = append(errs, m.checkConfigured()...)
	if err := m.Permissions.validate(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	return nil
}

func (r *Runtime) validate() []error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	paths := map[string]string{"": r.Command.Path}
	if r.Mode != "" && r.Mode != "process" && r.Mode != RuntimeDeno {
		bad("runtime.mode: expected process or deno")
	}
	if r.IsDeno() {
		errs = append(errs, r.validateSource()...)
		paths = nil
	} else if r.Source != nil {
		bad("runtime.source: only supported with mode deno")
	}
	if r.Command.ByPlatform != nil && !r.IsDeno() {
		paths = r.Command.ByPlatform
		if len(paths) == 0 {
			bad("runtime.command: no platform")
		}
	}
	for platform, p := range paths {
		if platform != "" && !platformKey.MatchString(platform) {
			bad("runtime.command: platform %q is not <os>-<arch>", platform)
		}
		if err := checkCommandPath(p); err != nil {
			bad("runtime.command %q: %v", p, err)
		}
	}
	if r.RPC != "" && r.RPC != "stdio" && r.RPC != "none" {
		bad("runtime.rpc %q: stdio or none", r.RPC)
	}
	if !r.SpeaksRPC() && !r.HTTP {
		bad("runtime: rpc none and no http: the hub could not talk to it")
	}
	if res := r.Resources; res != nil {
		if res.MemoryMB != 0 && (res.MemoryMB < ResourcesMemoryMin || res.MemoryMB > ResourcesMemoryMax) {
			bad("runtime.resources.memory_mb %d: %d–%d", res.MemoryMB, ResourcesMemoryMin, ResourcesMemoryMax)
		}
		if res.CPUPercent != 0 && (res.CPUPercent < ResourcesCPUMin || res.CPUPercent > ResourcesCPUMax) {
			bad("runtime.resources.cpu_percent %d: %d–%d", res.CPUPercent, ResourcesCPUMin, ResourcesCPUMax)
		}
	}
	return append(errs, checkEnv("runtime.env", r.Env)...)
}

func checkEnv(at string, env map[string]string) []error {
	var errs []error
	for k := range env {
		if !envPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("%s: %q is not a variable name", at, k))
		}
		if compat.ReservedEnv(k) {
			errs = append(errs, fmt.Errorf("%s: %q is the hub's to set", at, k))
		}
		// The dynamic loader's variables reach the sandbox's own launcher (bwrap, systemd-run, sandbox-exec), which
		// starts outside the sandbox: a library preloaded there would run unconfined.
		if strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_") {
			errs = append(errs, fmt.Errorf("%s: %q is not allowed (the dynamic loader's)", at, k))
		}
	}
	return errs
}

func (r *NodeRuntime) validate(m *Manifest) []error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if r.Builtin() {
		bad("node.command: %q is only for the hub's own built-in extensions", NodeBuiltin)
	} else if len(r.Command.ByPlatform) == 0 {
		bad("node.command: one program per platform (\"darwin-arm64\": \"node/bin/x\")")
	}
	for platform, p := range r.Command.ByPlatform {
		sys, _, _ := strings.Cut(platform, "-")
		if !platformKey.MatchString(platform) || !slices.Contains(nodeSystems, sys) {
			bad("node.command: platform %q is not darwin-<arch> or linux-<arch>", platform)
		}
		if IsBuiltinCommand(p) {
			bad("node.command: the hub's own binary is not a plugin's program on computers")
		} else if err := checkCommandPath(p); err != nil {
			bad("node.command %q: %v", p, err)
		}
	}
	if r.RPC != "" && r.RPC != "stdio" && r.RPC != "none" {
		bad("node.rpc %q: stdio or none", r.RPC)
	}
	if !r.SpeaksRPC() && !r.HTTP {
		bad("node: rpc none and no http: the hub could not talk to it")
	}
	if r.KeepRunning != "" && r.KeepRunning != KeepLease && r.KeepRunning != KeepAlways {
		bad("node.keep_running %q: lease or always", r.KeepRunning)
	}
	errs = append(errs, checkEnv("node.env", r.Env)...)
	seen := map[string]bool{}
	for _, name := range r.Secrets {
		if !slices.ContainsFunc(m.Secrets, func(s SecretSpec) bool { return s.Name == name }) {
			bad("node.secrets: %q is not one of secrets", name)
		}
		if seen[name] {
			bad("node.secrets: %q twice", name)
		}
		seen[name] = true
	}
	for _, h := range r.Permissions.Network {
		if !hostPattern.MatchString(h) || len(h) > 253 {
			bad("node.permissions.network: %q is not a host name (lowercase, no scheme, port or path; *. allowed first)", h)
		}
	}
	f := m.NodeMachines()
	switch {
	case r.Machines == "":
		bad("node.machines: the configuration field that picks the computers")
	case f == nil:
		bad("node.machines: configuration has no field %q", r.Machines)
	case f.Type != "array" || f.Items == nil || f.Items.Format != "machine":
		bad("configuration.%s: picks the computers: an array of format machine", r.Machines)
	case len(f.Items.OS) == 0 || slices.ContainsFunc(f.Items.OS, func(sys string) bool { return !slices.Contains(r.Systems(), sys) }):
		bad("configuration.%s: x-os %v: node.command has programs for %v", r.Machines, f.Items.OS, r.Systems())
	}
	return errs
}

// NodeMachines is the configuration field that picks the computers of the node program; nil without one.
func (m *Manifest) NodeMachines() *Field {
	if m.NodeProgram() == nil || m.Node.Machines == "" {
		return nil
	}
	sc, err := ParseSchema(m.Configuration)
	if err != nil || sc == nil {
		return nil
	}
	for i := range sc.Fields {
		if sc.Fields[i].Name == m.Node.Machines {
			return &sc.Fields[i]
		}
	}
	return nil
}

// checkCommandPath: a path inside the package (relative, no ..), or the hub's own binary.
func checkCommandPath(p string) error {
	switch {
	case p == "":
		return errors.New("empty")
	case IsBuiltinCommand(p):
		return nil
	case strings.HasPrefix(p, "/") || strings.Contains(p, `\`):
		return errors.New("must be relative to the package")
	case path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == "..":
		return errors.New("must stay inside the package")
	}
	return nil
}

// Activation is what starts the program: on_call when the manifest names nothing.
func (m *Manifest) Activation() []string {
	if len(m.ActivationEvents) == 0 && m.Runtime != nil {
		return []string{OnCall}
	}
	return m.ActivationEvents
}

// Items are the ids of the manifest's contributions to point (nil when it has none there).
func (m *Manifest) Items(point string) []string {
	var items []json.RawMessage
	if json.Unmarshal(m.Contributes[point], &items) != nil {
		return nil
	}
	item, ok := ItemOf(point)
	if !ok {
		return nil
	}
	var out []string
	for _, raw := range items {
		if id, err := item(raw); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// Connectors are the manifest's contributions to connectors.
func (m *Manifest) Connectors() []ConnectorItem {
	var out []ConnectorItem
	_ = json.Unmarshal(m.Contributes["connectors"], &out)
	return out
}

// SendsAsUser are the connectors that say they send messages in the user's name on their own (ConnectorItem.SendsAsUser).
func (m *Manifest) SendsAsUser() []ConnectorItem {
	var out []ConnectorItem
	for _, c := range m.Connectors() {
		if c.SendsAsUser {
			out = append(out, c)
		}
	}
	return out
}

// Recalls are the connectors that say they can take back what they sent (the capability recall): the hub then asks the
// program to, once the user agreed on their own device. Not something to agree to: it adds nothing the program can start.
func (m *Manifest) Recalls() []ConnectorItem {
	var out []ConnectorItem
	for _, c := range m.Connectors() {
		if slices.Contains(c.Capabilities, "recall") {
			out = append(out, c)
		}
	}
	return out
}

// APIs are the manifest's contributions to api.
func (m *Manifest) APIs() []APIItem {
	var out []APIItem
	_ = json.Unmarshal(m.Contributes["api"], &out)
	return out
}

// Messaging are the manifest's contributions to messaging: its messaging sources.
func (m *Manifest) Messaging() []wire.MessagingItem {
	var out []wire.MessagingItem
	_ = json.Unmarshal(m.Contributes["messaging"], &out)
	return out
}

// Assistants are the manifest's contributions to assistant.
func (m *Manifest) Assistants() []AssistantItem {
	var out []AssistantItem
	_ = json.Unmarshal(m.Contributes["assistant"], &out)
	return out
}

// MCPServers are the manifest's contributions to mcp_servers.
func (m *Manifest) MCPServers() []MCPServerItem {
	var out []MCPServerItem
	_ = json.Unmarshal(m.Contributes["mcp_servers"], &out)
	return out
}

// UIs are the manifest's contributions to ui.
func (m *Manifest) UIs() []UIItem {
	var out []UIItem
	_ = json.Unmarshal(m.Contributes["ui"], &out)
	return out
}

// Shortcuts are the manifest's contributions to shortcuts.
func (m *Manifest) Shortcuts() []ShortcutItem {
	var out []ShortcutItem
	_ = json.Unmarshal(m.Contributes["shortcuts"], &out)
	return out
}

// SurfaceShortcuts is the surface of an extension with shortcuts: its actions in Shortcuts and Siri.
const SurfaceShortcuts = "shortcuts"

// Surfaces are the slots outside its own settings the plugin draws in (UISlot.Shown), each once, in UISlots' order,
// then shortcuts when it has some: the user is shown them before installing and agrees again when an upgrade adds one.
func (m *Manifest) Surfaces() []string {
	var out []string
	for _, sl := range UISlots {
		if sl.Shown && slices.ContainsFunc(m.UIs(), func(u UIItem) bool { return u.Placement == sl.Name }) {
			out = append(out, sl.Name)
		}
	}
	if len(m.Shortcuts()) > 0 {
		out = append(out, SurfaceShortcuts)
	}
	return out
}

// Events are the manifest's contributions to events.
func (m *Manifest) Events() []EventsItem {
	var out []EventsItem
	_ = json.Unmarshal(m.Contributes["events"], &out)
	return out
}

// ErrorReports are the manifest's contributions to error_reports.
func (m *Manifest) ErrorReports() []ErrorReportsItem {
	var out []ErrorReportsItem
	_ = json.Unmarshal(m.Contributes["error_reports"], &out)
	return out
}

// AuthRelays are the manifest's contributions to auth_relay.
func (m *Manifest) AuthRelays() []AuthRelayItem {
	var out []AuthRelayItem
	_ = json.Unmarshal(m.Contributes["auth_relay"], &out)
	return out
}

// Topics are the events the plugin is told about, each once.
func (m *Manifest) Topics() []string {
	var out []string
	for _, e := range m.Events() {
		for _, t := range e.Subscribe {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Quotas are the manifest's contributions to quota.
func (m *Manifest) Quotas() []QuotaItem {
	var out []QuotaItem
	_ = json.Unmarshal(m.Contributes["quota"], &out)
	return out
}

// AgentSources are the manifest's contributions to agent_sources.
func (m *Manifest) AgentSources() []AgentSourceItem {
	var out []AgentSourceItem
	_ = json.Unmarshal(m.Contributes["agent_sources"], &out)
	return out
}

// Providers are the manifest's contributions to providers.
func (m *Manifest) Providers() []ProviderItem {
	var out []ProviderItem
	_ = json.Unmarshal(m.Contributes["providers"], &out)
	return out
}

// Channels are the plugin's contributions to channel.
func (m *Manifest) Channels() []channel.Item {
	var out []channel.Item
	_ = json.Unmarshal(m.Contributes["channel"], &out)
	return out
}

// Contributions are the ids by extension point, the points this hub has only.
func (m *Manifest) Contributions() map[string][]string {
	out := map[string][]string{}
	for name := range m.Contributes {
		if ids := m.Items(name); len(ids) > 0 {
			out[name] = ids
		}
	}
	return out
}

// Kind is the first extension point the plugin contributes to, by a fixed order: what GET /api/v1/plugins has always
// said as "kind" (voice for a voice plugin).
func (m *Manifest) Kind() string {
	for _, name := range []string{"voice", "connectors", "channel", "api", "quota", "providers", "agent_sources", "assistant", "ui", "events"} {
		if len(m.Items(name)) > 0 {
			return name
		}
	}
	keys := sortedKeys(m.Contributes)
	if len(keys) > 0 {
		return keys[0]
	}
	if len(m.ExtensionPack) > 0 {
		return "extension_pack"
	}
	return ""
}

// Lint are warnings that do not stop an install: extension points this hub does not have, the hub's full token asked
// the old way, configuration properties that look like secrets.
func (m *Manifest) Lint() []string {
	var out []string
	for _, name := range sortedKeys(m.Contributes) {
		if _, ok := ItemOf(name); !ok {
			out = append(out, i18n.T(i18n.En, "extension-status.unknown_point", i18n.Args{"name": name}))
		}
	}
	if m.Permissions.FullHub() {
		out = append(out, i18n.T(i18n.En, "extension-status.legacy_full_token"))
	}
	if s, err := ParseSchema(m.Configuration); err == nil && s != nil {
		for _, f := range s.Fields {
			if looksSecret(f.Name) {
				out = append(out, i18n.T(i18n.En, "extension-status.plaintext_secret", i18n.Args{"name": f.Name}))
			}
		}
	}
	return out
}

func looksSecret(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"password", "passwd", "token", "secret", "api_key", "apikey", "private_key"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
