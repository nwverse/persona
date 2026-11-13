package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// SourceDev is an extension being written, run from its own directory by `personad extension dev` (docs/extensions/extensions.md
// "Command line"): kept in memory only (never in the database, gone when the hub restarts), on, its declared permissions
// granted (it is the owner's own code on the owner's machine), its settings and secrets those the command sent.
const SourceDev = "dev"

// AddDev registers (or replaces) the extension being written in dir, with these settings and secrets. Its package is
// checked as an install's is (its program for this platform is in dir); its id may not be another source's.
func (r *Registry) AddDev(dir string, config json.RawMessage, secrets map[string]string) (*Extension, error) {
	return r.AddDevContext(context.Background(), dir, config, secrets)
}

func (r *Registry) AddDevContext(ctx context.Context, dir string, config json.RawMessage, secrets map[string]string) (*Extension, error) {
	unlock, err := r.beginOperation()
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := os.ReadFile(ManifestPath(dir))
	if err != nil {
		return nil, fmt.Errorf("没有清单：%w", err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := r.checkPackageContext(ctx, m, dir); err != nil {
		return nil, err
	}
	if err := CheckTriageRules(m); err != nil {
		return nil, err
	}
	schema, err := ParseSchema(m.Configuration)
	if err != nil {
		return nil, err
	}
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	if err := schema.Check(config); err != nil {
		return nil, fmt.Errorf("配置：%w", err)
	}
	for name := range secrets {
		if !m.hasSecret(name) {
			return nil, fmt.Errorf("清单里没有密钥 %s", name)
		}
	}
	translations, l10nWarnings := ReadTranslations(dir) // read before the lock: it is file I/O
	r.mu.Lock()
	cur := r.extensions[m.ID]
	if cur != nil && cur.Record.Source != SourceDev {
		r.mu.Unlock()
		return nil, fmt.Errorf("已经有一个叫 %s 的扩展（%s）：开发时换一个 id，或先停用、卸载它", m.ID, cur.Record.Source)
	}
	if err := r.conflicts(m); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if err := r.checkDependencyGraph(m); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	raw, _ := json.Marshal(m)
	now := r.now()
	p := &Extension{Record: Record{ID: m.ID, Source: SourceDev, Origin: "dev:" + dir, Version: m.Version, Manifest: raw,
		Granted: m.Permissions, GrantedDigest: Digest(m), Enabled: true, Config: config, InstalledAt: now, UpdatedAt: now},
		Manifest: m, Full: m, Schema: schema, Dir: dir, Warnings: m.Lint()}
	p.L10n = translations
	p.Warnings = append(p.Warnings, l10nWarnings...)
	if r.devSecrets == nil {
		r.devSecrets = map[string]map[string]string{}
	}
	r.extensions[m.ID], r.devSecrets[m.ID] = p, secrets
	r.mu.Unlock()
	kind := "installed"
	if cur != nil {
		kind = "upgraded"
	}
	unlock()
	r.changed(Change{ID: m.ID, Kind: kind, Old: cur})
	return p, nil
}

// RestartDev runs the extension being written again (its program was rebuilt).
func (r *Registry) RestartDev(id string) error {
	if p := r.Get(id); p == nil || p.Record.Source != SourceDev {
		return fmt.Errorf("没有开发中的扩展 %s", id)
	}
	r.changed(Change{ID: id, Kind: "config"})
	return nil
}

// RemoveDev takes the extension being written away.
func (r *Registry) RemoveDev(id string) error {
	unlock, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer unlock()
	r.mu.Lock()
	p := r.extensions[id]
	if p == nil || p.Record.Source != SourceDev {
		r.mu.Unlock()
		return errors.New("没有开发中的扩展 " + id)
	}
	delete(r.extensions, id)
	delete(r.devSecrets, id)
	r.mu.Unlock()
	unlock()
	r.changed(Change{ID: id, Kind: "removed", Old: p})
	return nil
}

func (m *Manifest) hasSecret(name string) bool {
	for _, s := range m.Secrets {
		if s.Name == name {
			return true
		}
	}
	return false
}
