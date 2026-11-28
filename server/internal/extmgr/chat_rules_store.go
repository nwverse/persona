package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/nwverse/persona/server/internal/connector"
)

// Where the user's per-chat rules are kept (#5278, chat_rules.go): one setting of the manager's database, a JSON array of
// connector.UserRule, written whole. The settings are PutSetting / Setting like the bots' and the channels' (the manager's
// own `settings` table; the hub's are in hub_settings). No table, no migration, nothing in manager.json.
//
// To empty it, back to a gate that asks as the hand-written rules say: delete the row (`DELETE FROM settings WHERE key =
// 'connector_chat_rules'` on the manager's database) and restart the manager, which holds what it read; or drop the rules
// one by one from the app.
const (
	chatRulesKey = "connector_chat_rules"
	// chatRulesMost is the most rules kept: each chat the user stops being asked about is one, and a list that only
	// grows is a list nobody reads. One more is refused (errChatRulesFull); dropping one makes room.
	chatRulesMost = 500
)

// errChatRulesFull is what Add says at chatRulesMost rules (and errNoDatabase, the connectors' too, when the manager has
// no database to keep them in).
var errChatRulesFull = fmt.Errorf("there are %d rules already: drop one first", chatRulesMost)

// settingRuleStore is the userRuleStore that keeps the rules in the manager's setting chatRulesKey. It reads the
// manager's store as it is at the time (tests, and a manager that gets one later, replace it), and keeps a lock of its
// own: an Add or a Remove reads the setting, changes it and writes it all back, so two at once would lose one.
type settingRuleStore struct {
	h  *Manager
	mu sync.Mutex
}

// read is the rules kept. A database that cannot be read is an error. A setting that is not a list at all is no rules,
// said in the log: the gate asks, which is where it started. In a list, an element that is not a rule (a null too) is
// left out, one by one and said in the log: the others are kept, and the next write is the list without it (a setting
// someone edited by hand with one slip does not lose the rest).
func (s *settingRuleStore) read(ctx context.Context) ([]connector.UserRule, error) {
	if s.h.store == nil {
		return nil, errNoDatabase
	}
	var raw json.RawMessage
	if _, err := s.h.store.Setting(ctx, chatRulesKey, &raw); err != nil {
		return nil, fmt.Errorf("read %s: %w", chatRulesKey, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		s.h.logf("user rules: %s is not a list (%v): taken as none, the gate asks", chatRulesKey, err)
		return nil, nil
	}
	var list []connector.UserRule
	for i, e := range elements {
		var u *connector.UserRule // a null leaves it nil: no rule either
		err := json.Unmarshal(e, &u)
		if err == nil && u == nil {
			err = errors.New("null")
		}
		if err != nil {
			s.h.logf("user rules: element %d of %s is not a rule (%v): left out, and gone at the next change", i, chatRulesKey, err)
			continue
		}
		list = append(list, *u)
	}
	return list, nil
}

func (s *settingRuleStore) write(ctx context.Context, list []connector.UserRule) error {
	if list == nil {
		list = []connector.UserRule{}
	}
	return s.h.store.PutSetting(ctx, chatRulesKey, list)
}

// List are the rules kept, as the setting has them: none for a setting that is no list, a list without the elements that
// are no rules (both said in the log), an error for a database that cannot be read, which the manager takes as "no rules
// yet" (the gate asks) and tries again at the next change.
func (s *settingRuleStore) List(ctx context.Context) ([]connector.UserRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(ctx)
}

// Add keeps a rule: one for a chat that is kept already stays as it is. A rule that is not a one-chat allow is refused,
// whoever made it, and so is the 501st.
func (s *settingRuleStore) Add(ctx context.Context, rule connector.UserRule) error {
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("not a rule of one chat: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(ctx) // a database that cannot be read must not be written over with a list of one
	if err != nil {
		return err
	}
	if slices.ContainsFunc(list, func(u connector.UserRule) bool { return u.ID() == rule.ID() }) {
		return nil
	}
	if len(list) >= chatRulesMost {
		return errChatRulesFull
	}
	return s.write(ctx, append(list, rule))
}

// Put upserts a rule by its ID: replacing an existing rule of that chat in place (for example when updating TTL,
// max_uses or used_count, or renewing an expired/exhausted rule) or appending it when new.
func (s *settingRuleStore) Put(ctx context.Context, rule connector.UserRule) error {
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("not a rule of one chat: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(ctx)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(list, func(u connector.UserRule) bool { return u.ID() == rule.ID() }); i >= 0 {
		list = slices.Clone(list)
		list[i] = rule
		for j := len(list) - 1; j > i; j-- {
			if list[j].ID() == rule.ID() {
				list = slices.Delete(list, j, j+1)
			}
		}
		return s.write(ctx, list)
	}
	if len(list) >= chatRulesMost {
		return errChatRulesFull
	}
	return s.write(ctx, append(list, rule))
}

// Remove drops the rule with id, and says whether there was one. Every rule of that chat goes, however it got in.
func (s *settingRuleStore) Remove(ctx context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(ctx)
	if err != nil {
		return false, err
	}
	kept := slices.DeleteFunc(slices.Clone(list), func(u connector.UserRule) bool { return u.ID() == id })
	if len(kept) == len(list) {
		return false, nil
	}
	return true, s.write(ctx, kept)
}
