package extmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
)

// The user's per-chat rules (#5278, docs/extensions/connectors.md "Sent as the user"): from a row of the ledger the user says
// "don't ask again in this chat", which adds a rule that lets writes into that one chat through without a card
// (connector.ChatRuleFor). This file is what the manager does with them: a store behind a small interface (the manager's
// setting, chat_rules_store.go), read at start; after each change they are applied at once, without a restart, in the
// two places a write is gated:
//
//   - the connectors' Registry (connector.Registry.SetUserRules), for the assistant's tool writes (GitHub's comments);
//   - what the node's Lark gate is served (assistantLark): the node asks for it about once a minute, so a change
//     reaches a write there within its next pull.
//
// The routes that make and drop them are in chat_rules_routes.go.

// userRuleStore keeps the rules the user made, whatever it is made of. Only the manager reads and writes it.
type userRuleStore interface {
	// List are the rules kept, in the order they were added.
	List(ctx context.Context) ([]connector.UserRule, error)
	// Add keeps a rule: one for a chat that is kept already (the same ID) stays as it is.
	Add(ctx context.Context, rule connector.UserRule) error
	// Remove drops the rule with id, and says whether there was one.
	Remove(ctx context.Context, id string) (bool, error)
}

// errNoRuleStore is what changing the user's rules says while there is nowhere to keep them.
var errNoRuleStore = errors.New("no place to keep the user's rules")

// chatRuleOfRow is the rule that lets writes into the chat a ledger row went to through without a card, or a
// *connector.NoChatRuleError saying why the row offers none (mail, a reply, an extension's own record…). A Lark row is
// the gated call of a command, whichever gate it was (every gate with credentials is recorded as "lark"), and a rule is
// only for the Lark gate: the node names the gate's command in every write it tells of, so a row that names none, or
// one of another command, is not one to make a rule from.
func (h *Manager) chatRuleOfRow(row db.SentAsUser) (connector.UserRule, error) {
	return chatRuleOfRowFor(row, func() []string { return h.extensions.gateCommands(connector.Lark) })
}

// chatRuleOfRowFor is chatRuleOfRow with the commands of the Lark gates given as a function that is called only when a
// row needs them (a page of the ledger asks of every row).
func chatRuleOfRowFor(row db.SentAsUser, larkCommands func() []string) (connector.UserRule, error) {
	if row.Connector == connector.Lark && isGateCall(row.Action) {
		// A row whose target does not say a command (nothing, null, {}, [], a number, a command that is not text) is not
		// the node's report of a gated write: it is data that is wrong, and nothing is let through on it.
		var t struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(row.Target, &t)
		if t.Command == "" {
			return connector.UserRule{}, &connector.NoChatRuleError{Code: connector.NoChatCommand,
				Reason: "the row does not say which command's gate the write went through: no rule is made from it"}
		}
		if !slices.Contains(larkCommands(), t.Command) {
			return connector.UserRule{}, &connector.NoChatRuleError{Code: connector.NoChatCommand,
				Reason: fmt.Sprintf("the write went through the command %q, which is not the Lark gate's: no rule is made from it", t.Command)}
		}
	}
	return connector.ChatRuleFor(row.Connector, row.Action, row.Target)
}

// isGateCall says whether a row's action is the gated call of a command, "METHOD /path" (the node's report), as against
// a tool's name or an extension's own record.
func isGateCall(action string) bool {
	method, path, ok := strings.Cut(action, " ")
	if !ok || method == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	for _, r := range method {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// userRules is the manager's view of the user's rules: the ones the store had when they were last read, changed by
// each change the manager made to it (the manager is the only writer), so a change is applied without asking the store
// again, and one that went into the store is never left unapplied because reading it back failed. mu guards it and
// serialises the changes: two at once apply in the order they were kept.
type userRules struct {
	mu     sync.Mutex
	loaded bool // the store was read once
	list   []connector.UserRule
	// snap is list as it was last applied, for the readers (the ledger page, the list): they do not wait for mu, which is
	// held while the store is written. nil until the rules were read.
	snap atomic.Pointer[[]connector.UserRule]
}

// chatRuleStoreTime is how long a change waits for the store, all of it from taking mu to the answer: mu is held
// meanwhile, and a database that hangs must not hold the next change up for ever. A variable so that a test can make it
// short; nothing else sets it.
var chatRuleStoreTime = 15 * time.Second

// errRuleStillStored is what a removal says when the rule is off in the gate and the store would not drop it: asking
// again finishes it.
var errRuleStillStored = errors.New("it is off for now but still stored: ask again")

// validChatRules are the rules of list that a gate could run (connector.UserRule.Validate), one to a chat, the first: a
// setting someone edited by hand may hold others, which stay in it and are not shown or applied.
func validChatRules(list []connector.UserRule) []connector.UserRule {
	var out []connector.UserRule
	seen := map[string]bool{}
	for _, u := range list {
		if u.Validate() == nil && !seen[u.ID()] {
			seen[u.ID()] = true
			out = append(out, u)
		}
	}
	return out
}

// startConnectorGate makes the connectors' registry with the gate's rules: the hand-written ones (connector_gate) and
// the user's, which the store has.
func (h *Manager) startConnectorGate(ctx context.Context) {
	h.connectors = h.newConnectors(h.cfg.ConnectorGate)
	if err := h.loadUserRules(ctx); err != nil && !errors.Is(err, errNoDatabase) {
		h.logf("user rules: %v: the gate asks as the hand-written rules say, until they can be read (a ledger page or a change tries again)", err)
	}
}

// loadUserRules reads the user's rules from the store and applies them. A store that cannot be read leaves the
// rules as they were (none, at start) and says why.
func (h *Manager) loadUserRules(ctx context.Context) error {
	h.userRules.mu.Lock()
	defer h.userRules.mu.Unlock()
	if h.ruleStore == nil {
		return nil
	}
	list, err := h.ruleStore.List(ctx)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	h.userRules.loaded, h.userRules.list = true, validChatRules(list)
	h.applyUserRules()
	return nil
}

// applyUserRules sets the rules in h.userRules on the Registry, with userRules.mu held. A rule that is not a one-chat
// allow is left out (and said); the Registry is what the node's Lark gate is served from.
func (h *Manager) applyUserRules() {
	snap := slices.Clone(h.userRules.list)
	h.userRules.snap.Store(&snap)
	if h.connectors == nil {
		return
	}
	if err := h.connectors.SetUserRules(h.userRules.list); err != nil {
		h.logf("user rules: %v", err)
	}
}

// readyForChange says, with userRules.mu held, whether the user's rules can be changed: there is a store, and its rules
// were read (a change is made on top of them, so one that could not be read yet is read now).
func (h *Manager) readyForChange(ctx context.Context) error {
	if h.ruleStore == nil {
		return errNoRuleStore
	}
	if !h.userRules.loaded {
		list, err := h.ruleStore.List(ctx)
		if err != nil {
			return fmt.Errorf("read the rules: %w", err)
		}
		h.userRules.loaded, h.userRules.list = true, validChatRules(list)
		h.applyUserRules()
	}
	return nil
}

// addChatRule keeps the rule for the chat a ledger row went to and applies it at once, without a restart. It returns
// the rule and whether it is new, or the *connector.NoChatRuleError that says why the row offers none; an error from the
// store leaves the rules as they were. Adding a chat that has a rule is no change: the rule it has is returned, as it was
// made.
func (h *Manager) addChatRule(ctx context.Context, row db.SentAsUser) (rule connector.UserRule, created bool, err error) {
	rule, err = h.chatRuleOfRow(row)
	if err != nil {
		return connector.UserRule{}, false, err
	}
	rule.At, rule.Row, rule.Summary = time.Now().UTC(), row.ID, cutRunes(row.Summary, sentSummaryMost)
	h.userRules.mu.Lock()
	defer h.userRules.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, chatRuleStoreTime)
	defer cancel()
	if err := h.readyForChange(ctx); err != nil {
		return connector.UserRule{}, false, err
	}
	if err := h.ruleStore.Add(ctx, rule); err != nil {
		return connector.UserRule{}, false, fmt.Errorf("keep the rule: %w", err)
	}
	if i := slices.IndexFunc(h.userRules.list, func(u connector.UserRule) bool { return u.ID() == rule.ID() }); i >= 0 {
		return h.userRules.list[i], false, nil
	}
	h.userRules.list = append(slices.Clone(h.userRules.list), rule)
	h.applyUserRules()
	return rule, true, nil
}

// removeChatRuleKey drops the rule that chatRuleKey names (what the API gives a client) and applies that at once,
// without a restart: the next write into the chat is asked about again. had is false when there was none. The rule is
// taken out of the gate first, and when the store then fails to drop it the error says so (errRuleStillStored): the
// rule is off for now (the user asked for that), still stored, and comes back at the next start unless the removal is
// asked for again. It returns the rule it dropped, for the audit; the whole of it, reading the store included, is held
// to chatRuleStoreTime.
func (h *Manager) removeChatRuleKey(ctx context.Context, key string) (rule connector.UserRule, had bool, err error) {
	h.userRules.mu.Lock()
	defer h.userRules.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, chatRuleStoreTime)
	defer cancel()
	if err := h.readyForChange(ctx); err != nil {
		return connector.UserRule{}, false, err
	}
	if i := slices.IndexFunc(h.userRules.list, func(u connector.UserRule) bool { return chatRuleKey(u) == key }); i >= 0 {
		rule = h.userRules.list[i]
	} else {
		// Not in force: a rule an earlier removal took out of the gate but the store would not drop is still stored, and
		// asking again has to be able to finish it. Only a rule a gate could run is dropped this way: what a hand-edited
		// setting holds besides is not a thing the app names.
		stored, err := h.ruleStore.List(ctx)
		if err != nil {
			return connector.UserRule{}, false, fmt.Errorf("read the rules: %w", err)
		}
		i = slices.IndexFunc(stored, func(u connector.UserRule) bool { return u.Validate() == nil && chatRuleKey(u) == key })
		if i < 0 {
			return connector.UserRule{}, false, nil
		}
		rule = stored[i]
	}
	had, err = h.removeLocked(ctx, rule.ID())
	return rule, had, err
}

// removeLocked drops the rule with id from the gate and then from the store, with userRules.mu held and ctx the change's
// own time limit.
func (h *Manager) removeLocked(ctx context.Context, id string) (had bool, err error) {
	if err := h.readyForChange(ctx); err != nil {
		return false, err
	}
	n := len(h.userRules.list)
	h.userRules.list = slices.DeleteFunc(slices.Clone(h.userRules.list), func(u connector.UserRule) bool { return u.ID() == id })
	h.applyUserRules()
	was := len(h.userRules.list) != n
	had, err = h.ruleStore.Remove(ctx, id)
	if err != nil {
		return was, fmt.Errorf("drop the rule: %w (%w)", err, errRuleStillStored)
	}
	return had || was, nil
}

// chatRules are the user's rules as the manager holds them (read from the store when that has not happened yet, within
// chatRuleStoreTime as a change is: the ledger page and a recall's answer wait for it), newest last.
func (h *Manager) chatRules(ctx context.Context) ([]connector.UserRule, error) {
	if snap := h.userRules.snap.Load(); snap != nil { // read, so no waiting for a change that is in the store's hands
		return slices.Clone(*snap), nil
	}
	h.userRules.mu.Lock()
	defer h.userRules.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, chatRuleStoreTime)
	defer cancel()
	if err := h.readyForChange(ctx); err != nil {
		return nil, err
	}
	return slices.Clone(h.userRules.list), nil
}

// chatRuleKey names a rule to a client: opaque and safe in a path (a GitHub rule's chat has a "/" and a "#"), the same for
// the same chat. The rule's own ID is what the store goes by.
func chatRuleKey(u connector.UserRule) string {
	sum := sha256.Sum256([]byte(u.ID()))
	return "cr_" + hex.EncodeToString(sum[:8])
}
