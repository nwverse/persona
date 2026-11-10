package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Inbox to-do rules are manager-owned settings. The item version ledger, thread map and decision log live beside the
// rules so the feature survives a manager restart without adding a table or exposing bookkeeping in a to-do note.
const (
	todoRulesKey       = "inbox_todo_rules"
	todoRuleKeep       = 14 * 24 * time.Hour
	todoRuleMaxRules   = 50
	todoRuleMaxPrompt  = 4000
	todoRuleMaxItems   = 2000
	todoRuleMaxThreads = 1000
	todoRuleMaxLog     = 500
	todoRuleModelWait  = 90 * time.Second
)

// TodoRule is one user-authored rule. Empty Sources or Kinds match every source or kind; an item whose sender is in
// SkipSenders (a bot's name, say) never matches.
type TodoRule struct {
	ID      string   `json:"-"` // manager bookkeeping; the public rule shape stays name/enabled/sources/kinds/prompt
	Name    string   `json:"name"`
	Enabled *bool    `json:"enabled,omitempty"`
	Sources []string `json:"sources,omitempty"`
	Kinds   []string `json:"kinds,omitempty"`
	// SkipSenders are sender names (as the Inbox item shows them) this rule leaves alone.
	SkipSenders []string `json:"skip_senders,omitempty"`
	Prompt      string   `json:"prompt"`
}

func (r TodoRule) enabled() bool { return r.Enabled == nil || *r.Enabled }

func (r TodoRule) matches(it *InboxItem) bool {
	return r.enabled() && matchesTodoRuleValue(r.Sources, it.Source) && matchesTodoRuleValue(r.Kinds, it.Kind) &&
		!slices.Contains(r.SkipSenders, strings.TrimSpace(it.Sender)) && strings.TrimSpace(r.Prompt) != ""
}

func matchesTodoRuleValue(values []string, value string) bool {
	return len(values) == 0 || slices.Contains(values, value)
}

type TodoRuleConfig struct {
	Rules  []TodoRule `json:"rules"`
	DryRun bool       `json:"dry_run"`
}

// TodoRuleDecision is the user-visible decision log. It contains metadata and the model's short reason, never the
// Inbox body.
type TodoRuleDecision struct {
	At       time.Time `json:"at"`
	ItemID   string    `json:"item_id"`
	Source   string    `json:"source,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Sender   string    `json:"sender,omitempty"`
	Title    string    `json:"title,omitempty"`
	RuleID   string    `json:"rule_id,omitempty"`
	RuleName string    `json:"rule_name,omitempty"`
	Verdict  string    `json:"verdict"`
	Reason   string    `json:"reason,omitempty"`
	TodoID   string    `json:"todo_id,omitempty"`
}

type todoRuleProcessed struct {
	ID       string    `json:"id"`
	Last     string    `json:"last"`
	TodoID   string    `json:"todo_id,omitempty"`
	Complete bool      `json:"complete"`
	At       time.Time `json:"at"`
}

type todoRulesState struct {
	Config    TodoRuleConfig      `json:"config"`
	Processed []todoRuleProcessed `json:"processed,omitempty"`
	Threads   map[string]string   `json:"threads,omitempty"`
	Decisions []TodoRuleDecision  `json:"decisions,omitempty"`
}

type todoRulesBook struct {
	mu         sync.Mutex
	processing sync.Mutex
	rerun      bool
	loaded     bool
	state      todoRulesState
}

func (s *Service) ensureTodoRulesLoaded(ctx context.Context) error {
	b := &s.todoRules
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.loaded {
		return nil
	}
	b.state = todoRulesState{Config: TodoRuleConfig{}, Threads: map[string]string{}}
	if st := s.store(); st != nil {
		var kept todoRulesState
		if _, err := st.Setting(ctx, todoRulesKey, &kept); err != nil {
			return err
		}
		if kept.Threads == nil {
			kept.Threads = map[string]string{}
		}
		// Rule IDs are manager bookkeeping and stay out of the public settings JSON. Recreate the positional IDs when
		// loading an older setting so decisions still carry a usable rule reference after a restart.
		for i := range kept.Config.Rules {
			if strings.TrimSpace(kept.Config.Rules[i].ID) == "" {
				kept.Config.Rules[i].ID = fmt.Sprintf("rule_%d", i+1)
			}
		}
		b.state = kept
	}
	b.loaded = true
	s.pruneTodoRulesLocked(time.Now().UTC())
	return nil
}

func (s *Service) saveTodoRulesLocked(ctx context.Context) error {
	if st := s.store(); st != nil {
		return st.PutSetting(ctx, todoRulesKey, s.todoRules.state)
	}
	return nil
}

func (s *Service) pruneTodoRulesLocked(now time.Time) {
	cutoff := now.Add(-todoRuleKeep)
	b := &s.todoRules.state
	b.Processed = slices.DeleteFunc(b.Processed, func(p todoRuleProcessed) bool { return !p.At.IsZero() && p.At.Before(cutoff) })
	b.Decisions = slices.DeleteFunc(b.Decisions, func(d TodoRuleDecision) bool { return !d.At.IsZero() && d.At.Before(cutoff) })
	if len(b.Processed) > todoRuleMaxItems {
		b.Processed = b.Processed[len(b.Processed)-todoRuleMaxItems:]
	}
	if len(b.Decisions) > todoRuleMaxLog {
		b.Decisions = b.Decisions[len(b.Decisions)-todoRuleMaxLog:]
	}
	if len(b.Threads) > todoRuleMaxThreads {
		// Thread mappings are only bookkeeping; keep the most recent mappings represented by processed entries.
		keep := map[string]bool{}
		for i := len(b.Processed) - 1; i >= 0 && len(keep) < todoRuleMaxThreads; i-- {
			if b.Processed[i].TodoID != "" {
				keep[b.Processed[i].TodoID] = true
			}
		}
		for k, v := range b.Threads {
			if !keep[v] {
				delete(b.Threads, k)
			}
		}
	}
}

func normalizeTodoRule(r TodoRule, i int) (TodoRule, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		r.ID = fmt.Sprintf("rule_%d", i+1)
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return TodoRule{}, i18n.Err("assistant-todo_rules.rule_name", i18n.Args{"index": i + 1})
	}
	r.Prompt = strings.TrimSpace(r.Prompt)
	if r.Prompt == "" {
		return TodoRule{}, i18n.Err("assistant-todo_rules.rule_prompt", i18n.Args{"index": i + 1})
	}
	if len([]rune(r.Prompt)) > todoRuleMaxPrompt {
		return TodoRule{}, i18n.Err("assistant-todo_rules.rule_prompt_long", i18n.Args{"index": i + 1, "max": todoRuleMaxPrompt})
	}
	normalize := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, v := range in {
			v = strings.TrimSpace(v)
			if v != "" && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		return out
	}
	r.Sources, r.Kinds, r.SkipSenders = normalize(r.Sources), normalize(r.Kinds), normalize(r.SkipSenders)
	return r, nil
}

func normalizeTodoRuleConfig(cfg TodoRuleConfig) (TodoRuleConfig, error) {
	if len(cfg.Rules) > todoRuleMaxRules {
		return TodoRuleConfig{}, i18n.Err("assistant-todo_rules.too_many", i18n.Args{"max": todoRuleMaxRules})
	}
	out := TodoRuleConfig{DryRun: cfg.DryRun, Rules: make([]TodoRule, 0, len(cfg.Rules))}
	for i, r := range cfg.Rules {
		n, err := normalizeTodoRule(r, i)
		if err != nil {
			return TodoRuleConfig{}, err
		}
		if slices.ContainsFunc(out.Rules, func(x TodoRule) bool { return x.ID == n.ID }) {
			return TodoRuleConfig{}, i18n.Err("assistant-todo_rules.duplicate", i18n.Args{"name": n.Name})
		}
		out.Rules = append(out.Rules, n)
	}
	return out, nil
}

func (s *Service) todoRulesView(ctx context.Context) (TodoRuleConfig, error) {
	if err := s.ensureTodoRulesLoaded(ctx); err != nil {
		return TodoRuleConfig{}, err
	}
	s.todoRules.mu.Lock()
	defer s.todoRules.mu.Unlock()
	return cloneTodoRuleConfig(s.todoRules.state.Config), nil
}

func cloneTodoRuleConfig(in TodoRuleConfig) TodoRuleConfig {
	out := in
	if in.Rules == nil {
		out.Rules = []TodoRule{}
	}
	out.Rules = make([]TodoRule, len(in.Rules))
	for i, r := range in.Rules {
		out.Rules[i] = r
		out.Rules[i].Sources = slices.Clone(r.Sources)
		out.Rules[i].Kinds = slices.Clone(r.Kinds)
		out.Rules[i].SkipSenders = slices.Clone(r.SkipSenders)
		if r.Enabled != nil {
			on := *r.Enabled
			out.Rules[i].Enabled = &on
		}
	}
	return out
}

func (s *Service) todoRuleRoutes() {
	s.handle("GET /api/v1/todo-rules", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.todoRulesView(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, cfg)
	})
	s.handle("PUT /api/v1/todo-rules", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-todo_rules.read_only"))
			return
		}
		var in TodoRuleConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		cfg, err := normalizeTodoRuleConfig(in)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if err := s.ensureTodoRulesLoaded(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		s.todoRules.mu.Lock()
		s.todoRules.state.Config = cfg
		err = s.saveTodoRulesLocked(r.Context())
		s.todoRules.mu.Unlock()
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		s.todoRulesSoon(r.Context())
		apierr.WriteJSON(w, cfg)
	})
	s.handle("GET /api/v1/todo-rules/decisions", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ensureTodoRulesLoaded(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		limit := 100
		if n := r.URL.Query().Get("limit"); n != "" {
			_, _ = fmt.Sscanf(n, "%d", &limit)
		}
		limit = max(1, min(limit, todoRuleMaxLog))
		s.todoRules.mu.Lock()
		processed, decisions, threads := len(s.todoRules.state.Processed), len(s.todoRules.state.Decisions), len(s.todoRules.state.Threads)
		s.pruneTodoRulesLocked(time.Now().UTC())
		if processed != len(s.todoRules.state.Processed) || decisions != len(s.todoRules.state.Decisions) || threads != len(s.todoRules.state.Threads) {
			if err := s.saveTodoRulesLocked(r.Context()); err != nil {
				s.todoRules.mu.Unlock()
				apierr.Write(w, http.StatusServiceUnavailable, err)
				return
			}
		}
		all := s.todoRules.state.Decisions
		out := make([]TodoRuleDecision, 0, min(limit, len(all)))
		for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
			out = append(out, all[i])
		}
		s.todoRules.mu.Unlock()
		apierr.WriteJSON(w, map[string]any{"decisions": out})
	})
}

func (s *Service) todoRulesSoon(ctx context.Context) {
	if s.ensureTodoRulesLoaded(ctx) != nil {
		return
	}
	s.todoRules.mu.Lock()
	on := len(s.todoRules.state.Config.Rules) > 0
	s.todoRules.mu.Unlock()
	if !on || !s.todoRules.processing.TryLock() {
		if on {
			s.todoRules.mu.Lock()
			s.todoRules.rerun = true
			s.todoRules.mu.Unlock()
		}
		return
	}
	go func() {
		defer s.todoRules.processing.Unlock()
		for {
			s.processTodoRules(context.WithoutCancel(ctx))
			s.todoRules.mu.Lock()
			rerun := s.todoRules.rerun
			s.todoRules.rerun = false
			on := len(s.todoRules.state.Config.Rules) > 0
			s.todoRules.mu.Unlock()
			if !rerun || !on {
				return
			}
		}
	}()
}

func (s *Service) processTodoRules(ctx context.Context) {
	if err := s.inboxReady(ctx); err != nil || s.ensureTodoRulesLoaded(ctx) != nil {
		return
	}
	s.inbox.mu.Lock()
	items := make([]*InboxItem, 0, len(s.inbox.items))
	for _, it := range s.inbox.items {
		items = append(items, it)
	}
	s.inbox.mu.Unlock()
	for _, it := range items {
		if err := s.processTodoRuleItem(ctx, it); err != nil {
			s.logf("inbox to-do rules: %s: %v", it.ID, err)
		}
	}
}

func todoRuleVersion(it *InboxItem) string {
	if it.Last != "" {
		return it.Last
	}
	if !it.UpdatedAt.IsZero() {
		return it.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func (s *Service) processedTodoRule(id, last string) (todoRuleProcessed, bool) {
	s.todoRules.mu.Lock()
	defer s.todoRules.mu.Unlock()
	for i := len(s.todoRules.state.Processed) - 1; i >= 0; i-- {
		p := s.todoRules.state.Processed[i]
		if p.ID == id && p.Last == last {
			return p, true
		}
	}
	return todoRuleProcessed{}, false
}

func (s *Service) recordTodoRuleDecision(ctx context.Context, it *InboxItem, rule TodoRule, verdict, reason, todoID string) error {
	now := time.Now().UTC()
	d := TodoRuleDecision{At: now, ItemID: it.ID, Source: it.Source, Kind: it.Kind, Sender: clip(oneLine(it.Sender), 100), Title: clip(oneLine(it.Title), 200), RuleID: rule.ID, RuleName: rule.Name,
		Verdict: verdict, Reason: clip(oneLine(strings.TrimSpace(reason)), 200), TodoID: todoID}
	s.todoRules.mu.Lock()
	s.todoRules.state.Decisions = append(s.todoRules.state.Decisions, d)
	s.pruneTodoRulesLocked(now)
	err := s.saveTodoRulesLocked(ctx)
	s.todoRules.mu.Unlock()
	return err
}

func (s *Service) markTodoRuleProcessed(ctx context.Context, id, last, todoID string, complete bool) error {
	now := time.Now().UTC()
	s.todoRules.mu.Lock()
	p := todoRuleProcessed{ID: id, Last: last, TodoID: todoID, Complete: complete, At: now}
	for i := range s.todoRules.state.Processed {
		if s.todoRules.state.Processed[i].ID == id && s.todoRules.state.Processed[i].Last == last {
			s.todoRules.state.Processed = append(s.todoRules.state.Processed[:i], s.todoRules.state.Processed[i+1:]...)
			break
		}
	}
	s.todoRules.state.Processed = append(s.todoRules.state.Processed, p)
	s.pruneTodoRulesLocked(now)
	err := s.saveTodoRulesLocked(ctx)
	s.todoRules.mu.Unlock()
	return err
}

func (s *Service) todoRuleThread(it *InboxItem) string {
	thread := ""
	switch {
	case it.Connector != nil:
		thread = it.Connector.Thread
	case it.Lark != nil:
		thread = migratedThread(it)
	case it.Mail != nil:
		thread = it.Mail.Thread
	case it.Doc != nil:
		thread = "doc:" + it.Doc.NoticeID
	case it.Moment != nil:
		thread = "moment:" + it.Moment.PostID
	}
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return ""
	}
	return it.Source + "\x00" + thread
}

func (s *Service) todoRuleInput(it *InboxItem) prompts.TodoRule {
	return prompts.TodoRule{Source: it.Source, Kind: it.Kind, Sender: it.Sender, Title: it.Title, Context: clip(it.Context, 12000)}
}

type todoRuleVerdict struct {
	IsTodo  bool   `json:"is_todo"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Reason  string `json:"reason"`
}

func (s *Service) askTodoRule(ctx context.Context, rule TodoRule, it *InboxItem) (todoRuleVerdict, error) {
	cctx, cancel := context.WithTimeout(ctx, todoRuleModelWait)
	defer cancel()
	out := todoRuleVerdict{}
	p := s.todoRuleInput(it)
	p.RulePrompt = rule.Prompt
	input, _ := json.Marshal(struct {
		Source string `json:"source"`
		Kind   string `json:"kind"`
		Sender string `json:"sender"`
		Title  string `json:"title"`
		Text   string `json:"context"`
	}{Source: p.Source, Kind: p.Kind, Sender: p.Sender, Title: p.Title, Text: p.Context})
	err := s.purposeModel(purposeTodoRules).JSONMax(cctx, prompts.RenderTodoRule(p), string(input), 768, &out)
	if err != nil {
		return todoRuleVerdict{}, err
	}
	out.Title = clip(oneLine(strings.TrimSpace(out.Title)), 80)
	out.Summary = clip(oneLine(strings.TrimSpace(out.Summary)), 500)
	out.Reason = clip(oneLine(strings.TrimSpace(out.Reason)), 200)
	if !out.IsTodo {
		out.Title, out.Summary = "", ""
	}
	return out, nil
}

func (s *Service) processTodoRuleItem(ctx context.Context, it *InboxItem) error {
	last := todoRuleVersion(it)
	if last == "" {
		return nil
	}
	if p, ok := s.processedTodoRule(it.ID, last); ok && p.Complete {
		return nil
	}

	s.todoRules.mu.Lock()
	cfg := cloneTodoRuleConfig(s.todoRules.state.Config)
	s.todoRules.mu.Unlock()
	if len(cfg.Rules) == 0 {
		return nil
	}
	// Thread mappings can point at an existing to-do after a manager restart. Load the to-do book before looking up
	// that mapping, otherwise todoNow would look only at the empty in-memory cache and create a duplicate.
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}

	// A pending parent write from a previous attempt is completed without another model call or note append.
	if p, ok := s.processedTodoRule(it.ID, last); ok && p.TodoID != "" && !p.Complete {
		if err := s.setTodoRuleParent(ctx, it, p.TodoID); err != nil {
			return err
		}
		if err := s.recordTodoRuleDecision(ctx, it, TodoRule{}, "created", "", p.TodoID); err != nil {
			return err
		}
		return s.markTodoRuleProcessed(ctx, it.ID, last, p.TodoID, true)
	}

	s.inbox.mu.Lock()
	done, parented, muted, awaitingTriage := !it.open(), it.Parent != nil, mutedBy(s.inbox.mutes, it) != nil, it.TriagedAt != it.Last
	if done || parented || muted || awaitingTriage {
		s.inbox.mu.Unlock()
		verdict, reason := "skipped", i18n.T(s.lang(ctx), "assistant-todo_rules.skipped_other")
		if parented {
			reason = i18n.T(s.lang(ctx), "assistant-todo_rules.skipped_parent")
		} else if done {
			reason = i18n.T(s.lang(ctx), "assistant-todo_rules.skipped_done")
		} else if muted {
			reason = i18n.T(s.lang(ctx), "assistant-todo_rules.skipped_muted")
		} else {
			return nil
		}
		_ = s.recordTodoRuleDecision(ctx, it, TodoRule{}, verdict, reason, "")
		return s.markTodoRuleProcessed(ctx, it.ID, last, "", true)
	}
	rules := make([]TodoRule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		if r.matches(it) {
			rules = append(rules, r)
		}
	}
	snapshot := *it
	s.inbox.mu.Unlock()

	if len(rules) == 0 {
		if err := s.recordTodoRuleDecision(ctx, &snapshot, TodoRule{}, "no_rule", i18n.T(s.lang(ctx), "assistant-todo_rules.no_rule"), ""); err != nil {
			return err
		}
		return s.markTodoRuleProcessed(ctx, snapshot.ID, last, "", true)
	}
	for _, rule := range rules {
		v, err := s.askTodoRule(ctx, rule, &snapshot)
		if err != nil {
			_ = s.recordTodoRuleDecision(ctx, &snapshot, rule, "error", err.Error(), "")
			return err
		}
		if !v.IsTodo {
			if err := s.recordTodoRuleDecision(ctx, &snapshot, rule, "not_todo", v.Reason, ""); err != nil {
				return err
			}
			continue
		}
		if cfg.DryRun {
			if err := s.recordTodoRuleDecision(ctx, &snapshot, rule, "dry_run", v.Reason, ""); err != nil {
				return err
			}
			return s.markTodoRuleProcessed(ctx, snapshot.ID, last, "", true)
		}
		todoID, err := s.applyTodoRule(ctx, &snapshot, last, rule, v)
		if err != nil {
			_ = s.recordTodoRuleDecision(ctx, &snapshot, rule, "error", err.Error(), todoID)
			return err
		}
		return nil
	}
	return s.markTodoRuleProcessed(ctx, snapshot.ID, last, "", true)
}

func todoRuleNote(it *InboxItem, v todoRuleVerdict) string {
	if strings.TrimSpace(v.Summary) != "" {
		return strings.TrimSpace(v.Summary)
	}
	if strings.TrimSpace(it.Preview) != "" {
		return strings.TrimSpace(it.Preview)
	}
	return strings.TrimSpace(it.Title)
}

func (s *Service) applyTodoRule(ctx context.Context, it *InboxItem, last string, rule TodoRule, v todoRuleVerdict) (string, error) {
	thread := s.todoRuleThread(it)
	var todoID string
	if thread != "" {
		s.todoRules.mu.Lock()
		todoID = s.todoRules.state.Threads[thread]
		s.todoRules.mu.Unlock()
		if todoID != "" {
			if t, ok := s.todoNow(todoID); !ok || t.DoneAt != nil {
				todoID = ""
			} else {
				notes := strings.TrimSpace(t.Notes)
				note := todoRuleNote(it, v)
				if note != "" {
					if notes != "" {
						notes += "\n\n"
					}
					notes += note
				}
				if _, err := s.editTodo(ctx, todoID, db.TodoEdit{Notes: &notes}); err != nil {
					return todoID, err
				}
				if err := s.markTodoRuleProcessed(ctx, it.ID, last, todoID, false); err != nil {
					return todoID, err
				}
				if err := s.setTodoRuleParent(ctx, it, todoID); err != nil {
					return todoID, err
				}
				if err := s.recordTodoRuleDecision(ctx, it, rule, "appended", v.Reason, todoID); err != nil {
					return todoID, err
				}
				return todoID, s.markTodoRuleProcessed(ctx, it.ID, last, todoID, true)
			}
		}
	}

	title := strings.TrimSpace(v.Title)
	if title == "" {
		title = strings.TrimSpace(it.Title)
	}
	if title == "" {
		title = i18n.T(s.lang(ctx), "assistant-todo_rules.fallback_title")
	}
	t, err := s.newTodo(ctx, title, todoRuleNote(it, v), "", "", false)
	if err != nil {
		return "", err
	}
	todoID = t.ID
	if thread != "" {
		s.todoRules.mu.Lock()
		s.todoRules.state.Threads[thread] = todoID
		err = s.saveTodoRulesLocked(ctx)
		s.todoRules.mu.Unlock()
		if err != nil {
			return todoID, err
		}
	}
	if err := s.markTodoRuleProcessed(ctx, it.ID, last, todoID, false); err != nil {
		return todoID, err
	}
	if err := s.setTodoRuleParent(ctx, it, todoID); err != nil {
		return todoID, err
	}
	if err := s.recordTodoRuleDecision(ctx, it, rule, "created", v.Reason, todoID); err != nil {
		return todoID, err
	}
	return todoID, s.markTodoRuleProcessed(ctx, it.ID, last, todoID, true)
}

func (s *Service) setTodoRuleParent(ctx context.Context, it *InboxItem, todoID string) error {
	if _, ok := s.todoNow(todoID); !ok {
		return db.ErrTodoNotFound
	}
	s.inbox.mu.Lock()
	current := it.Parent
	s.inbox.mu.Unlock()
	if current != nil {
		if current.Kind == ParentTodo && current.ID == todoID {
			return nil
		}
		return errors.New("inbox item already has a parent")
	}
	_, code, err := s.setInboxParentBy(ctx, it.ID, &InboxParent{Kind: ParentTodo, ID: todoID}, ParentByRule)
	if err != nil {
		return fmt.Errorf("set inbox parent (%d): %w", code, err)
	}
	return nil
}
