package extension

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Cloud host providers (docs/design/machine-cloud-ops.md): an extension's cloud_hosts contribution speaks for a
// vendor. It lists the instances of the user's accounts, says how each is, and starts, stops and restarts them, reads
// their addresses back, snapshots them and opens their console. The registry only says which contributions are
// installed and on, and holds the safety table of the standard actions; asking a program (cloud.instances,
// cloud.instance, cloud.action, cloud.snapshots) is the manager's (extmgr/extension_cloud.go), a write only after its
// safety gate passed. Core never names a vendor.

// CloudHostItem is a contribution to cloud_hosts.
type CloudHostItem = wire.CloudHostItem

// The bounds of one cloud_hosts item, the same as manifest.schema.json's CloudHostItem and CloudCustomAction.
const (
	cloudHostNameMost      = 40
	cloudCustomActionsMost = 8
	cloudCustomTitleMost   = 40
	cloudCustomConfirmMost = 200
)

// cloudActionSafety is the hub's gate for each standard action (the doc's "Actions and safety levels"). A program
// cannot lower it; custom actions carry their own in the manifest.
var cloudActionSafety = map[wire.CloudActionKind]wire.CloudSafety{
	wire.CloudActionKindStart:          wire.CloudSafetyConfirm,
	wire.CloudActionKindStop:           wire.CloudSafetyConfirm,
	wire.CloudActionKindReboot:         wire.CloudSafetyConfirm,
	wire.CloudActionKindHardReset:      wire.CloudSafetyDestructive,
	wire.CloudActionKindForceStop:      wire.CloudSafetyDestructive,
	wire.CloudActionKindSyncIp:         wire.CloudSafetyRead,
	wire.CloudActionKindSnapshotList:   wire.CloudSafetyRead,
	wire.CloudActionKindSnapshotCreate: wire.CloudSafetyConfirm,
	wire.CloudActionKindConsole:        wire.CloudSafetyRead,
}

// CloudStandardActions are the kinds an instance's actions may name: every CloudActionKind but custom, in the
// schema's order.
var CloudStandardActions = func() []wire.CloudActionKind {
	var out []wire.CloudActionKind
	for _, k := range wire.CloudActionKinds {
		if k != wire.CloudActionKindCustom {
			out = append(out, k)
		}
	}
	return out
}()

// cloudSafetyList names the safety levels for an error, in the schema's order.
var cloudSafetyList = func() string {
	names := make([]string, len(wire.CloudSafeties))
	for i, v := range wire.CloudSafeties {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}()

// CloudActionSafety is the safety level the hub holds a standard action to. ok is false for custom (whose level is
// the manifest's) and for anything that is not a known kind.
func CloudActionSafety(action wire.CloudActionKind) (safety wire.CloudSafety, ok bool) {
	safety, ok = cloudActionSafety[action]
	return safety, ok
}

// checkCloudHost holds one cloud_hosts item to the point's rules (Points' Item).
func checkCloudHost(it CloudHostItem) error {
	if !idPattern.MatchString(string(it.ID)) {
		return fmt.Errorf("cloud_hosts id %q: a-z0-9_-", it.ID)
	}
	if !shortText(it.Name, cloudHostNameMost) {
		return fmt.Errorf("cloud_hosts %s: a name of 1 to %d characters, no control characters", it.ID, cloudHostNameMost)
	}
	if !idPattern.MatchString(it.Vendor) {
		return fmt.Errorf("cloud_hosts %s: vendor %q: a-z0-9_-", it.ID, it.Vendor)
	}
	if it.CustomActions != nil && len(it.CustomActions) == 0 {
		return fmt.Errorf("cloud_hosts %s: custom_actions is left out or names at least one", it.ID)
	}
	if len(it.CustomActions) > cloudCustomActionsMost {
		return fmt.Errorf("cloud_hosts %s: at most %d custom_actions", it.ID, cloudCustomActionsMost)
	}
	for i, a := range it.CustomActions {
		if !idPattern.MatchString(string(a.ID)) {
			return fmt.Errorf("cloud_hosts %s: custom action id %q: a-z0-9_-", it.ID, a.ID)
		}
		if slices.ContainsFunc(it.CustomActions[:i], func(b wire.CloudCustomAction) bool { return b.ID == a.ID }) {
			return fmt.Errorf("cloud_hosts %s: custom action %q twice", it.ID, a.ID)
		}
		if !shortText(a.Title, cloudCustomTitleMost) {
			return fmt.Errorf("cloud_hosts %s: custom action %s: a title of 1 to %d characters, no control characters", it.ID, a.ID, cloudCustomTitleMost)
		}
		if !a.Safety.Valid() {
			return fmt.Errorf("cloud_hosts %s: custom action %s: safety %q: one of %s", it.ID, a.ID, a.Safety, cloudSafetyList)
		}
		if a.Safety == wire.CloudSafetyRead && a.Confirm == "" {
			continue
		}
		if !shortText(a.Confirm, cloudCustomConfirmMost) {
			return fmt.Errorf("cloud_hosts %s: custom action %s: confirm, the sentence the confirmation asks, of 1 to %d characters, no control characters (needed unless safety is read)",
				it.ID, a.ID, cloudCustomConfirmMost)
		}
	}
	return nil
}

// CloudHosts are the manifest's cloud_hosts contributions that hold to the point's rules.
func (m *Manifest) CloudHosts() []CloudHostItem {
	var items, out []CloudHostItem
	_ = json.Unmarshal(m.Contributes["cloud_hosts"], &items)
	for _, it := range items {
		if checkCloudHost(it) == nil {
			out = append(out, it)
		}
	}
	return out
}

// cloudHostEntry is one installed cloud_hosts contribution, as EachCloudHost tells it.
type cloudHostEntry struct {
	extension string
	item      CloudHostItem
	on        bool
}

// cloudHostsOf are p's cloud_hosts contributions as installed (Full), each on when it takes effect. Called with r.mu
// held; id is p's canonical id.
func cloudHostsOf(id string, p *Extension) []cloudHostEntry {
	if p == nil || p.Full == nil {
		return nil
	}
	var on []string
	if p.Record.Enabled && p.Problem == "" && p.Manifest != nil {
		on = p.Manifest.Items("cloud_hosts")
	}
	var out []cloudHostEntry
	for _, it := range p.Full.CloudHosts() {
		it.CustomActions = slices.Clone(it.CustomActions)
		out = append(out, cloudHostEntry{extension: id, item: it, on: slices.Contains(on, string(it.ID))})
	}
	return out
}

// EachCloudHost tells f every installed cloud_hosts contribution, on or off, by extension id and then in manifest
// order: the extension's canonical id (after a rename), the item as its manifest declares it (its name, vendor and
// custom actions with their safety levels), and on: the extension is enabled, has no problem and the contribution is
// switched on. Whether its program runs is the manager's to say. f is called without the registry's lock held.
func (r *Registry) EachCloudHost(f func(extension string, it CloudHostItem, on bool)) {
	r.mu.Lock()
	var all []cloudHostEntry
	for _, id := range r.sortedIDs() {
		all = append(all, cloudHostsOf(id, r.extensions[id])...)
	}
	r.mu.Unlock()
	for _, e := range all {
		f(e.extension, e.item, e.on)
	}
}
