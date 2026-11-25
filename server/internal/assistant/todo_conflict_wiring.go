package assistant

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/nwverse/persona/server/internal/assistant/todo/conflict"
	"github.com/nwverse/persona/server/internal/proto"
)

// plannedWriteScopeInput carries a planned task together with optional write-scope
// conflict hints supplied on task_plan input (under todos_v2).
type plannedWriteScopeInput struct {
	Task          PlanTask
	WritePrefixes []string
	ChangedFiles  []string
	ForceParallel bool
}

// writeScopeHints holds structured write-scope hints parsed from free-form text
// (task prompts, context, or to-do notes).
type writeScopeHints struct {
	RepoKey       string
	WritePrefixes []string
	ChangedFiles  []string
	ForceParallel bool
}

type todoWriteScopeRecord struct {
	RepoKey       string
	WritePrefixes []string
	ChangedFiles  []string
	ForceParallel bool
}

// writeScopeTracker keeps per-Service in-memory write scopes and runtime changed files
// for dispatched todos under todos_v2, without adding DB migrations or modifying Service fields.
type writeScopeTracker struct {
	mu      sync.RWMutex
	records map[string]todoWriteScopeRecord
}

func newWriteScopeTracker() *writeScopeTracker {
	return &writeScopeTracker{records: make(map[string]todoWriteScopeRecord)}
}

func (w *writeScopeTracker) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	clear(w.records)
}

func (w *writeScopeTracker) set(todoID string, rec todoWriteScopeRecord) {
	if todoID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	prev := w.records[todoID]
	if rec.RepoKey != "" {
		prev.RepoKey = rec.RepoKey
	}
	if len(rec.WritePrefixes) > 0 {
		prev.WritePrefixes = slices.Clone(rec.WritePrefixes)
	}
	if len(rec.ChangedFiles) > 0 {
		prev.ChangedFiles = slices.Clone(rec.ChangedFiles)
	}
	if rec.ForceParallel {
		prev.ForceParallel = true
	}
	w.records[todoID] = prev
}

func (w *writeScopeTracker) setRuntimeChangedFiles(todoID string, files []string) {
	if todoID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	prev := w.records[todoID]
	prev.ChangedFiles = slices.Clone(files)
	w.records[todoID] = prev
}

func (w *writeScopeTracker) get(todoID string) (todoWriteScopeRecord, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	rec, ok := w.records[todoID]
	if !ok {
		return todoWriteScopeRecord{}, false
	}
	rec.WritePrefixes = slices.Clone(rec.WritePrefixes)
	rec.ChangedFiles = slices.Clone(rec.ChangedFiles)
	return rec, true
}

var (
	writeScopeRegistryMu sync.Mutex
	writeScopeRegistry   = map[weak.Pointer[Service]]*writeScopeTracker{}
)

func (s *Service) writeScopeTracker() *writeScopeTracker {
	if s == nil {
		return nil
	}
	key := weak.Make(s)
	writeScopeRegistryMu.Lock()
	defer writeScopeRegistryMu.Unlock()
	if tr, ok := writeScopeRegistry[key]; ok {
		return tr
	}
	tr := newWriteScopeTracker()
	writeScopeRegistry[key] = tr
	runtime.AddCleanup(s, func(k weak.Pointer[Service]) {
		writeScopeRegistryMu.Lock()
		delete(writeScopeRegistry, k)
		writeScopeRegistryMu.Unlock()
	}, key)
	return tr
}

// recordTodoRuntimeChangedFiles attaches runtime changed files (e.g. from git diff --name-only)
// to an active to-do for Layer 2 conflict detection under todos_v2.
// Strictly bypassed when todos_v2=false.
func (s *Service) recordTodoRuntimeChangedFiles(ctx context.Context, todoID string, files []string) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return
	}
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return
	}
	s.writeScopeTracker().setRuntimeChangedFiles(todoID, files)
}

// rememberPlannedWriteScopes records dispatched tasks' declared write scopes against their
// assigned to-do IDs so subsequent task_plan calls can detect same-repo overlaps against them.
// Strictly bypassed when todos_v2=false.
func (s *Service) rememberPlannedWriteScopes(ctx context.Context, started []PlanTask, inputs []plannedWriteScopeInput) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return
	}
	tr := s.writeScopeTracker()
	for i := range started {
		todoID := strings.TrimSpace(started[i].TodoID)
		if todoID == "" || i >= len(inputs) {
			continue
		}
		in := inputs[i]
		in.Task = started[i]
		hints := parseWriteScopeHints(in.Task.Prompt, in.Task.Context, in.Task.Goal)
		prefixes := mergeUniqueStrings(in.WritePrefixes, hints.WritePrefixes)
		changed := mergeUniqueStrings(in.ChangedFiles, hints.ChangedFiles)
		force := in.ForceParallel || hints.ForceParallel
		repoKey := plannedTaskRepoKey(in.Task, hints.RepoKey)
		if repoKey == "" && len(prefixes) == 0 && len(changed) == 0 && !force {
			continue
		}
		tr.set(todoID, todoWriteScopeRecord{
			RepoKey:       repoKey,
			WritePrefixes: prefixes,
			ChangedFiles:  changed,
			ForceParallel: force,
		})
	}
}

// detectPlannedWriteScopeConflicts evaluates static and runtime write-scope conflicts
// among planned tasks and active open same-repository to-dos using todo/conflict.Detect.
// Strictly gated behind todos_v2=true; returns nil immediately when todos_v2=false.
func (s *Service) detectPlannedWriteScopeConflicts(ctx context.Context, planned []plannedWriteScopeInput) []conflict.PairConflict {
	conflicts, _ := s.detectPlannedWriteScopeConflictsWithLabels(ctx, planned)
	return conflicts
}

// planWriteScopeConflicts formats any write-scope conflicts detected for planned tasks
// as a deterministic advisory block for task_plan responses. Returns "" when todos_v2=false
// or when no conflicts exist.
func (s *Service) planWriteScopeConflicts(ctx context.Context, planned []plannedWriteScopeInput) string {
	if s == nil || !s.settings(ctx).todosV2On() {
		return ""
	}
	conflicts, labels := s.detectPlannedWriteScopeConflictsWithLabels(ctx, planned)
	return formatPlannedWriteScopeConflicts(conflicts, labels)
}

func (s *Service) detectPlannedWriteScopeConflictsWithLabels(ctx context.Context, planned []plannedWriteScopeInput) ([]conflict.PairConflict, map[string]string) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return nil, nil
	}
	if len(planned) == 0 {
		return nil, nil
	}

	labels := make(map[string]string, len(planned))
	plannedIDs := make(map[string]bool, len(planned))
	skipExisting := make(map[string]bool, len(planned))
	scopes := make([]conflict.TaskScope, 0, len(planned))

	for i, in := range planned {
		id := strings.TrimSpace(in.Task.ReuseTodo)
		if id == "" {
			id = fmt.Sprintf("planned:%d", i+1)
		} else {
			skipExisting[id] = true
		}
		if c := strings.TrimSpace(in.Task.Continues); c != "" {
			skipExisting[c] = true
		}
		plannedIDs[id] = true
		title := strings.TrimSpace(in.Task.Title)
		if title != "" {
			if strings.HasPrefix(id, "planned:") {
				labels[id] = fmt.Sprintf("planned #%d (%s)", i+1, title)
			} else {
				labels[id] = fmt.Sprintf("%s (%s)", id, title)
			}
		} else {
			labels[id] = id
		}

		hints := parseWriteScopeHints(in.Task.Prompt, in.Task.Context, in.Task.Goal)
		repoKey := plannedTaskRepoKey(in.Task, hints.RepoKey)
		prefixes := mergeUniqueStrings(in.WritePrefixes, hints.WritePrefixes)
		changed := mergeUniqueStrings(in.ChangedFiles, hints.ChangedFiles)
		force := in.ForceParallel || hints.ForceParallel
		if repoKey == "" || (len(prefixes) == 0 && len(changed) == 0) {
			continue
		}
		scopes = append(scopes, conflict.TaskScope{
			TodoID:        id,
			RepoKey:       repoKey,
			WritePrefixes: prefixes,
			ChangedFiles:  changed,
			ForceParallel: force,
		})
	}

	if len(scopes) == 0 {
		return nil, labels
	}

	// Collect active open to-dos and include any same-repo write scopes / runtime changed files.
	if s.ensureTodosLoaded(ctx) == nil {
		var open []Todo
		s.todos.mu.RLock()
		for _, td := range s.todos.todos {
			if td.DoneAt != nil || td.CancelledAt != nil || skipExisting[td.ID] {
				continue
			}
			open = append(open, copyTodoLocked(td))
		}
		s.todos.mu.RUnlock()

		tr := s.writeScopeTracker()
		for _, td := range open {
			scope, ok := s.openTodoTaskScope(ctx, td, tr)
			if !ok {
				continue
			}
			if title := strings.TrimSpace(td.Title); title != "" {
				labels[td.ID] = fmt.Sprintf("%s (%s)", td.ID, title)
			} else {
				labels[td.ID] = td.ID
			}
			scopes = append(scopes, scope)
		}
	}

	all := conflict.Detect(scopes)
	if len(all) == 0 {
		return nil, labels
	}
	// Only report pairs involving at least one task from this plan.
	filtered := make([]conflict.PairConflict, 0, len(all))
	for _, c := range all {
		if plannedIDs[c.TodoA] || plannedIDs[c.TodoB] {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return nil, labels
	}
	return filtered, labels
}

func (s *Service) openTodoTaskScope(ctx context.Context, td Todo, tr *writeScopeTracker) (conflict.TaskScope, bool) {
	texts := []string{td.Notes}
	if td.Brief != nil {
		texts = append(texts, td.Brief.Goal)
	}
	hints := parseWriteScopeHints(texts...)

	var rec todoWriteScopeRecord
	if tr != nil {
		rec, _ = tr.get(td.ID)
	}

	prefixes := mergeUniqueStrings(rec.WritePrefixes, hints.WritePrefixes)
	changed := mergeUniqueStrings(rec.ChangedFiles, hints.ChangedFiles)
	force := rec.ForceParallel || hints.ForceParallel
	if len(prefixes) == 0 && len(changed) == 0 {
		return conflict.TaskScope{}, false
	}

	repoKey := hints.RepoKey
	if repoKey == "" {
		repoKey = rec.RepoKey
	}
	if repoKey == "" {
		repoKey = s.openTodoRepoKey(ctx, td)
	}
	if repoKey == "" {
		return conflict.TaskScope{}, false
	}

	return conflict.TaskScope{
		TodoID:        td.ID,
		RepoKey:       repoKey,
		WritePrefixes: prefixes,
		ChangedFiles:  changed,
		ForceParallel: force,
	}, true
}

func (s *Service) openTodoRepoKey(ctx context.Context, td Todo) string {
	// Prefer the most recent active agent's workspace, then any agent with a Cwd.
	for i := len(td.Agents) - 1; i >= 0; i-- {
		a := td.Agents[i]
		if a.active() && strings.TrimSpace(a.Cwd) != "" {
			if key := normalizeConflictRepoPathKey(a.MachineID, a.Cwd); key != "" {
				return key
			}
		}
	}
	for i := len(td.Agents) - 1; i >= 0; i-- {
		a := td.Agents[i]
		if strings.TrimSpace(a.Cwd) != "" {
			if key := normalizeConflictRepoPathKey(a.MachineID, a.Cwd); key != "" {
				return key
			}
		}
	}
	if strings.TrimSpace(td.PRURL) != "" {
		if repoKey, _, _, ok := parseMergeTrainPRURL(td.PRURL); ok {
			return repoKey
		}
	}
	if s != nil {
		if _, ok := s.store().(todoRepositoryStore); ok {
			if set, err := s.savedTodoRepositories(ctx, td.ID); err == nil {
				for _, r := range set.Repositories {
					if key := normalizeConflictRepoRef(r.SourceSnapshot.CloneURL); key != "" {
						return key
					}
					if key := normalizeConflictRepoRef(r.RepoID); key != "" {
						return key
					}
				}
			}
		}
	}
	return ""
}

func plannedTaskRepoKey(t PlanTask, explicit string) string {
	if key := normalizeConflictRepoRef(explicit); key != "" {
		return key
	}
	if strings.TrimSpace(t.Repo) != "" {
		if key := normalizeConflictRepoPathKey(t.Machine, t.Repo); key != "" {
			return key
		}
	}
	if t.WorkspaceResolved != nil && strings.TrimSpace(t.WorkspaceResolved.Path) != "" {
		machine := t.WorkspaceResolved.Machine
		if strings.TrimSpace(machine) == "" {
			machine = t.Machine
		}
		if key := normalizeConflictRepoPathKey(machine, t.WorkspaceResolved.Path); key != "" {
			return key
		}
	}
	if dir := strings.TrimSpace(taskRunDir(t)); dir != "" {
		if key := normalizeConflictRepoPathKey(t.Machine, dir); key != "" {
			return key
		}
	}
	return ""
}

func normalizeConflictRepoPathKey(machine, dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	cleaned := proto.NodeClean(dir)
	if cleaned == "" || cleaned == "." {
		return ""
	}
	machine = strings.ToLower(strings.TrimSpace(machine))
	if machine == "" {
		return cleaned
	}
	return machine + ":" + cleaned
}

func normalizeConflictRepoRef(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			p := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
			if p != "" {
				return strings.ToLower(u.Host) + "/" + strings.ToLower(p)
			}
		}
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if strings.HasPrefix(s, "/") {
		return path.Clean(s)
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(s, "/"), ".git"))
}

// parseWriteScopeHints extracts optional write-scope tags from task prompt/context/notes lines:
//   - write_prefixes: a/b, c/d  (aliases: write_prefix:, write_scope:)
//   - changed_files: a/b/x.go, c/d/y.go  (alias: changed_file:)
//   - force_parallel: true
//   - repo_key: github.com/org/repo
func parseWriteScopeHints(texts ...string) writeScopeHints {
	var out writeScopeHints
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, rawLine := range strings.Split(text, "\n") {
			line := strings.TrimSpace(rawLine)
			line = strings.TrimLeft(line, "-*> \t")
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			key, val, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(strings.Trim(key, "`*")))
			v := strings.TrimSpace(val)
			switch k {
			case "write_prefixes", "write_prefix", "write_scope":
				out.WritePrefixes = append(out.WritePrefixes, splitHintList(v)...)
			case "changed_files", "changed_file":
				out.ChangedFiles = append(out.ChangedFiles, splitHintList(v)...)
			case "force_parallel":
				switch strings.ToLower(strings.Trim(v, "`'\"")) {
				case "true", "1", "yes":
					out.ForceParallel = true
				}
			case "repo_key", "repo":
				if out.RepoKey == "" {
					out.RepoKey = normalizeConflictRepoRef(strings.Trim(v, "`'\""))
				}
			}
		}
	}
	out.WritePrefixes = mergeUniqueStrings(out.WritePrefixes)
	out.ChangedFiles = mergeUniqueStrings(out.ChangedFiles)
	return out
}

func splitHintList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(strings.Trim(p, "`'\""))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func mergeUniqueStrings(lists ...[]string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, list := range lists {
		for _, raw := range list {
			v := strings.TrimSpace(raw)
			if v == "" {
				continue
			}
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func formatPlannedWriteScopeConflicts(conflicts []conflict.PairConflict, labels map[string]string) string {
	if len(conflicts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWrite-scope conflicts (same repository):")
	for _, c := range conflicts {
		labelA := c.TodoA
		if l := labels[c.TodoA]; l != "" {
			labelA = l
		}
		labelB := c.TodoB
		if l := labels[c.TodoB]; l != "" {
			labelB = l
		}
		var details []string
		if len(c.StaticOverlaps) > 0 {
			details = append(details, "static overlaps: "+strings.Join(c.StaticOverlaps, ", "))
		}
		if len(c.RuntimeFiles) > 0 {
			details = append(details, "runtime files: "+strings.Join(c.RuntimeFiles, ", "))
		}
		var guidance string
		switch c.Action {
		case conflict.ActionWarnOverridden:
			guidance = "static overlap explicitly allowed via force_parallel"
		default:
			if c.Layer == conflict.LayerRuntime || c.Layer == conflict.LayerBoth {
				guidance = "runtime file intersection detected; downgrade to serial execution/merge (force_parallel does not override runtime intersections)"
			} else {
				guidance = "recommend running serially or setting force_parallel=true if write scopes are actually disjoint"
			}
		}
		fmt.Fprintf(&b, "\n- %s <-> %s [repo=%s, layer=%s, action=%s] (%s): %s",
			labelA, labelB, c.RepoKey, c.Layer, c.Action, strings.Join(details, "; "), guidance)
	}
	return b.String()
}
