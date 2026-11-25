package hub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func (s *settingsStore) Settings(_ context.Context, keys ...string) (map[string]json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]json.RawMessage{}
	for _, key := range keys {
		if raw, ok := s.vals[key]; ok {
			out[key] = append(json.RawMessage(nil), raw...)
		}
	}
	return out, nil
}

func (s *settingsStore) PutPushGradingBase(_ context.Context, baseKey, _, mirrorKey string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var base struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals[baseKey] = raw
	if base.Enabled != nil {
		raw, _ := json.Marshal(*base.Enabled)
		s.vals[mirrorKey] = raw
	}
	s.puts++
	return nil
}

func (s *settingsStore) PutPushGradingLayer(_ context.Context, _, key, _, manager, source string, value any, out any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var current map[string]any
	if existing, ok := s.vals[key]; ok {
		if err := json.Unmarshal(existing, &current); err != nil {
			return false, err
		}
		if current["manager"] != manager || current["source"] != source {
			return false, nil
		}
	}
	revision := 1
	if current != nil {
		revision = int(current["revision"].(float64)) + 1
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return false, err
	}
	object["revision"] = revision
	raw, err = json.Marshal(object)
	if err != nil {
		return false, err
	}
	s.vals[key] = raw
	s.puts++
	if out != nil {
		return true, json.Unmarshal(raw, out)
	}
	return true, nil
}

func (s *settingsStore) DeletePushGradingLayer(_ context.Context, _, key, _, manager, source string, revision int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.vals[key]
	if !ok {
		return false, nil
	}
	var current map[string]any
	if err := json.Unmarshal(raw, &current); err != nil {
		return false, err
	}
	if current["manager"] != manager || current["source"] != source || revision > 0 && int64(current["revision"].(float64)) != revision {
		return false, nil
	}
	delete(s.vals, key)
	return true, nil
}

func TestPushGradingLayerStoreRoundTripAndOwnership(t *testing.T) {
	ctx := context.Background()
	store := &settingsStore{vals: map[string][]byte{}}
	base := parseGradingTestConfig(t, `{"version":1,"enabled":false,"actions":{"P1":"push"}}`, false)
	if err := store.PutSetting(ctx, pushGradingBaseSettingKey, base); err != nil {
		t.Fatal(err)
	}
	layer := pushGradingLayer{
		Source: "extension:one", Manager: "manager-a", UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Config: parseGradingTestConfig(t, `{"version":1,"actions":{"P2":"push"}}`, true),
	}
	stored, err := writePushGradingLayer(ctx, store, layer)
	if err != nil || stored.Revision != 1 {
		t.Fatalf("first write: stored=%+v err=%v", stored, err)
	}
	stored, err = writePushGradingLayer(ctx, store, layer)
	if err != nil || stored.Revision != 2 {
		t.Fatalf("renewal: stored=%+v err=%v", stored, err)
	}
	other := layer
	other.Source = "extension:two"
	if _, err := writePushGradingLayer(ctx, store, other); !errors.Is(err, errPushGradingLayerTaken) {
		t.Fatalf("competing write error = %v", err)
	}
	snapshot, err := readPushGradingSnapshot(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Base == nil || snapshot.Layer == nil || snapshot.Layer.Revision != 2 {
		t.Fatalf("snapshot did not persist: %+v", snapshot)
	}
	if snapshot.Effective.Enabled || snapshot.Effective.Actions.P1 != "push" || snapshot.Effective.Actions.P2 != "push" {
		t.Fatalf("effective config is wrong: %+v", snapshot.Effective)
	}
	if snapshot.Effective.By["actions.P1"] != "base" || snapshot.Effective.By["actions.P2"] != "extension:one" {
		t.Fatalf("effective sources are wrong: %+v", snapshot.Effective.By)
	}
	deleted, err := deletePushGradingLayer(ctx, store, "manager-a", "extension:one", 1)
	if err != nil || deleted {
		t.Fatalf("stale delete: deleted=%v err=%v", deleted, err)
	}
	deleted, err = deletePushGradingLayer(ctx, store, "manager-a", "extension:one", 2)
	if err != nil || !deleted {
		t.Fatalf("current delete: deleted=%v err=%v", deleted, err)
	}
	snapshot, err = readPushGradingSnapshot(ctx, store)
	if err != nil || snapshot.Layer != nil || snapshot.Effective.Enabled {
		t.Fatalf("layer survived physical deletion: %+v err=%v", snapshot, err)
	}
}

func TestPushGradingLayerStorePreservesLegacySwitch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial bool
	}{{"disabled", false}, {"enabled", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			raw, _ := json.Marshal(tc.initial)
			store := &settingsStore{vals: map[string][]byte{gradingSwitchSettingKey: raw}}
			layer := pushGradingLayer{
				Source: "extension:one", Manager: "manager-a", UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Config: parseGradingTestConfig(t, `{"version":1,"actions":{"P2":"push"}}`, true),
			}
			if _, err := writePushGradingLayer(ctx, store, layer); err != nil {
				t.Fatal(err)
			}
			if got := string(store.vals[gradingSwitchSettingKey]); got != string(raw) {
				t.Fatalf("claim changed legacy switch: got=%s want=%s", got, raw)
			}
			stored, err := writePushGradingLayer(ctx, store, layer)
			if err != nil || stored.Revision != 2 {
				t.Fatalf("renewal: stored=%+v err=%v", stored, err)
			}
			if got := string(store.vals[gradingSwitchSettingKey]); got != string(raw) {
				t.Fatalf("renewal changed legacy switch: got=%s want=%s", got, raw)
			}
			deleted, err := deletePushGradingLayer(ctx, store, "manager-a", "extension:one", 2)
			if err != nil || !deleted {
				t.Fatalf("delete: deleted=%v err=%v", deleted, err)
			}
			if got := string(store.vals[gradingSwitchSettingKey]); got != string(raw) {
				t.Fatalf("delete changed legacy switch: got=%s want=%s", got, raw)
			}
			base := parseGradingTestConfig(t, `{"version":1,"enabled":null}`, false)
			if err := writePushGradingBase(ctx, store, base); err != nil {
				t.Fatal(err)
			}
			if got := string(store.vals[gradingSwitchSettingKey]); got != string(raw) {
				t.Fatalf("null base enabled changed legacy switch: got=%s want=%s", got, raw)
			}
		})
	}
}
