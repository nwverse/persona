package assistant

// Sorting the to-dos into knowledge-base projects in one go (#13026, docs/assistant/todos.md "Sorting into projects"):
// for the open to-dos in no project and not marked Temporary, a suggested project node (any live knowledge-base
// project) or the Temporary mark, with how sure it is and why. Rules first (the project of the direction above it, the
// project whose folders its Agents work in, a project its title names), then the kb.project model for every item, so
// it can confirm a rule, place what the rules could not, or say a to-do is a one-off. Nothing is written here: the
// owner ticks and changes, and todo_sort_batches.go writes what they confirm as one batch they can undo.

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Suggestion bounds.
const (
	sortMaxItems     = 80 // to-dos put before the model (the newest)
	sortMaxProjects  = 60 // projects offered to the model (the most recently updated)
	sortPreselect    = 0.6
	sortModelWait    = 40 * time.Second // the request waits for it; the apps' reads give up after 45 s
	sortModelTokens  = 4096
	sortReasonMax    = 120
	sortRuleIsSure   = 0.8 // a rule at least this sure is not moved by the model's other guess
	sureByDirection  = 0.85
	sureByDirs       = 0.8
	sureByTitle      = 0.6
	sortTitleNameMin = 2 // runes a project name needs before a title naming it counts
)

// sortItem is one to-do with the place suggested for it: Project a live project's slug, or Temporary; neither is no
// suggestion (the owner can still choose one). By is what suggested it: direction, dirs, title or model.
type sortItem struct {
	Todo        string  `json:"todo"`
	Title       string  `json:"title"`
	Detail      string  `json:"detail,omitempty"` // where its Agents work
	Project     string  `json:"project"`
	ProjectName string  `json:"project_name,omitempty"`
	Temporary   bool    `json:"temporary,omitempty"`
	Confidence  float64 `json:"confidence"`
	Reason      string  `json:"reason,omitempty"`
	By          string  `json:"by,omitempty"`
	Preselect   bool    `json:"preselect"`
}

// sortTarget is a project a suggestion (or the owner's change) can name.
type sortTarget struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// sortResult is the suggestions. Model: used, unavailable (none configured), failed, or none (nothing to ask it).
// Unsorted: every open to-do in no project and not Temporary (Items holds at most sortMaxItems of them); Sorted: the
// open ones already in a project.
type sortResult struct {
	Items    []sortItem   `json:"items"`
	Projects []sortTarget `json:"projects"`
	Model    string       `json:"model"`
	Unsorted int          `json:"unsorted"`
	Sorted   int          `json:"sorted"`
}

// unsortedTodo: open, in no project, not marked as a one-off. A concern (the owner's raised question) is left out: it
// closes by its report, not as project work. The web's "Sort N unsorted" counts by the same rule (web/src/lib/todo-sort.ts).
func unsortedTodo(t Todo) bool {
	return t.DoneAt == nil && t.Project == "" && !t.Temporary && !t.isConcern()
}

// openTodos are copies of the open to-dos, question to-dos (ask_todo.go) left out: they are no work to file.
func (s *Service) openTodos() []Todo {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	var out []Todo
	for _, t := range s.todos.todos {
		if t.DoneAt == nil && !isAskTodo(t.ID) {
			out = append(out, copyTodoLocked(t))
		}
	}
	return out
}

// sortCandidates are the projects a suggestion may name: live, not ignored, not done; the most recently updated first.
func sortCandidates(e *kb.Engine) []*kb.Project {
	var out []*kb.Project
	for _, p := range e.Store.Projects() {
		if p.Live() && p.Status != "ignored" && p.Status != "done" {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b *kb.Project) int {
		return cmp.Or(b.Updated.Compare(a.Updated), strings.Compare(a.Slug, b.Slug))
	})
	return out
}

// suggestSort makes the suggestions; lang is the reasons' language, model whether to ask the model.
func (s *Service) suggestSort(ctx context.Context, lang i18n.Lang, model bool) (sortResult, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return sortResult{}, errTodosUnavailable
	}
	e, err := s.kbGet(ctx)
	if err != nil {
		return sortResult{}, err
	}
	res := sortResult{Items: []sortItem{}, Projects: []sortTarget{}, Model: "none"}
	cands := sortCandidates(e)
	byslug := map[string]*kb.Project{}
	for _, p := range cands {
		byslug[p.Slug] = p
		res.Projects = append(res.Projects, sortTarget{Slug: p.Slug, Name: p.Name})
	}
	var todos []Todo
	for _, t := range s.openTodos() {
		switch {
		case unsortedTodo(t):
			res.Unsorted++
			todos = append(todos, t)
		case t.Project != "":
			res.Sorted++
		}
	}
	slices.SortStableFunc(todos, func(a, b Todo) int { return b.CreatedAt.Compare(a.CreatedAt) })
	todos = todos[:min(len(todos), sortMaxItems)]
	reason := func(key string, args i18n.Args) string { return i18n.T(lang, "assistant-todos.sort_reason_"+key, args) }
	for _, t := range todos {
		it := sortItem{Todo: t.ID, Title: clip(oneLine(t.Title), 200), Detail: todoDirs(t, 1)}
		s.sortRules(ctx, e, byslug, t, &it, reason)
		res.Items = append(res.Items, it)
	}
	if model && len(res.Items) > 0 {
		reply, err := s.sortModel(ctx, cands, todos)
		switch {
		case err == nil:
			res.Model = "used"
			mergeSortReply(res.Items, reply, byslug)
		case ai.IsModelUnavailable(err):
			res.Model = "unavailable"
		default:
			res.Model = "failed"
			s.logf("to-do sort: the model failed, the rules stand: %v", err)
		}
	}
	for i := range res.Items {
		it := &res.Items[i]
		if p := byslug[it.Project]; p != nil {
			it.ProjectName, it.Temporary = p.Name, false
		} else {
			it.Project = ""
		}
		if it.Project == "" && !it.Temporary {
			it.Confidence, it.By = 0, ""
		}
		it.Confidence = math.Round(it.Confidence*100) / 100
		it.Preselect = (it.Project != "" || it.Temporary) && it.Confidence >= sortPreselect
	}
	// the sure ones first, the ones with no suggestion last
	slices.SortStableFunc(res.Items, func(a, b sortItem) int {
		if ah, bh := a.Project != "" || a.Temporary, b.Project != "" || b.Temporary; ah != bh {
			if ah {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.Confidence, a.Confidence)
	})
	return res, nil
}

// sortRules places it by the rules, surest first: the direction above the to-do, its Agents' folders, its title.
func (s *Service) sortRules(ctx context.Context, e *kb.Engine, byslug map[string]*kb.Project, t Todo, it *sortItem, reason func(string, i18n.Args) string) {
	if _, d, ok := s.directionForTodo(ctx, t); ok && d.Project != "" {
		if p := e.FindProject(d.Project); p != nil && byslug[p.Slug] != nil {
			it.Project, it.Confidence, it.By = p.Slug, sureByDirection, "direction"
			it.Reason = reason("direction", i18n.Args{"name": p.Name})
			return
		}
	}
	var dirs []string
	dir := ""
	for _, a := range t.Agents {
		if a.Cwd == "" {
			continue
		}
		for _, p := range projectsForWork(e, a.MachineID, a.Cwd) {
			if byslug[p.Slug] != nil && !slices.Contains(dirs, p.Slug) {
				dirs = append(dirs, p.Slug)
				dir = cmp.Or(dir, shortHome(a.Cwd))
			}
		}
	}
	if len(dirs) == 1 {
		it.Project, it.Confidence, it.By = dirs[0], sureByDirs, "dirs"
		it.Reason = reason("dirs", i18n.Args{"name": byslug[dirs[0]].Name, "dir": dir})
		return
	}
	if slug := titleNames(t.Title, byslug, dirs); slug != "" {
		it.Project, it.Confidence, it.By = slug, sureByTitle, "title"
		it.Reason = reason("title", i18n.Args{"name": byslug[slug].Name})
	}
}

// titleNames is the one project whose name or alias the title holds (among only when several folders matched);
// the longest name wins, and two different projects with names of that length are no answer.
func titleNames(title string, byslug map[string]*kb.Project, among []string) string {
	low := strings.ToLower(title)
	best, bestLen, tie := "", 0, false
	for slug, p := range byslug {
		if len(among) > 1 && !slices.Contains(among, slug) {
			continue
		}
		for _, name := range append([]string{p.Name}, p.Aliases...) {
			name = strings.ToLower(strings.TrimSpace(name))
			n := utf8.RuneCountInString(name)
			if n < sortTitleNameMin || !wholeWord(low, name) {
				continue
			}
			switch {
			case n > bestLen:
				best, bestLen, tie = slug, n, false
			case n == bestLen && slug != best:
				tie = true
			}
		}
	}
	if tie {
		return ""
	}
	return best
}

// todoDirs are the folders t's Agents work in, as people read them, at most n.
func todoDirs(t Todo, n int) string {
	var out []string
	for _, a := range t.Agents {
		if d := shortHome(a.Cwd); d != "" && !slices.Contains(out, d) && len(out) < n {
			out = append(out, d)
		}
	}
	return strings.Join(out, ", ")
}

// shortHome writes a home directory's path with ~ (/Users/me/src → ~/src), as people read it.
func shortHome(dir string) string {
	for _, prefix := range []string{"/Users/", "/home/"} {
		if rest, ok := strings.CutPrefix(dir, prefix); ok {
			if _, after, ok := strings.Cut(rest, "/"); ok {
				return "~/" + after
			}
			return "~"
		}
	}
	return dir
}

// sortReply is the model's answer, read leniently.
type sortReply struct {
	Items []struct {
		Todo       string  `json:"todo"`
		Project    string  `json:"project"`
		Temporary  bool    `json:"temporary"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	} `json:"items"`
}

// sortModel asks the kb.project model where each to-do belongs.
func (s *Service) sortModel(ctx context.Context, cands []*kb.Project, todos []Todo) (sortReply, error) {
	language := answerLanguage(s.hello(ctx).Locale)
	in := prompts.TodoSortInput{Language: language}
	for _, p := range cands[:min(len(cands), sortMaxProjects)] {
		in.Projects = append(in.Projects, prompts.TodoSortProject{Slug: p.Slug, Name: oneLine(p.Name), OneLine: clip(oneLine(p.OneLine), 120),
			Dirs: strings.Join(p.Dirs[:min(len(p.Dirs), 3)], ", ")})
	}
	for _, t := range todos {
		in.Todos = append(in.Todos, prompts.TodoSortTodo{ID: t.ID, Title: clip(oneLine(t.Title), 200), Notes: clip(oneLine(t.Notes), 160), Dirs: todoDirs(t, 2)})
	}
	var reply sortReply
	cctx, cancel := context.WithTimeout(ctx, sortModelWait)
	defer cancel()
	err := s.purposeModel("kb.project").JSONMax(cctx, prompts.RenderTodoSort(prompts.TodoSort{Language: language, Owner: s.owner(ctx)}),
		prompts.RenderTodoSortInput(in), sortModelTokens, &reply)
	return reply, err
}

// mergeSortReply lays the model's answer over the rules: agreeing, the surer of the two with the model's reason
// (unless the rule's is about a direction); disagreeing, a sure rule stays and otherwise the surer answer wins.
// Unknown to-dos and projects are dropped.
func mergeSortReply(items []sortItem, reply sortReply, byslug map[string]*kb.Project) {
	at := map[string]int{}
	for i, it := range items {
		at[it.Todo] = i
	}
	for _, m := range reply.Items {
		i, ok := at[strings.TrimSpace(m.Todo)]
		if !ok {
			continue
		}
		it := &items[i]
		project := strings.TrimSpace(m.Project)
		temporary := m.Temporary && project == ""
		if project != "" && byslug[project] == nil {
			continue
		}
		if project == "" && !temporary {
			continue // the model has no suggestion: the rule's (or none) stands
		}
		conf := min(max(m.Confidence, 0), 1)
		why := clip(oneLine(m.Reason), sortReasonMax)
		switch {
		case project != "" && project == it.Project, temporary && it.Temporary:
			it.Confidence = max(it.Confidence, conf)
			if why != "" && it.By != "direction" {
				it.Reason = why
			}
		case (it.Project != "" || it.Temporary) && it.Confidence >= sortRuleIsSure:
			// the rule is sure; the model's other guess does not move it
		case conf > it.Confidence:
			it.Project, it.Temporary, it.Confidence, it.Reason, it.By = project, temporary, conf, why, "model"
		}
	}
}

func (s *Service) postTodoSortSuggest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model *bool `json:"model"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
	}
	res, err := s.suggestSort(r.Context(), i18n.FromRequest(r), in.Model == nil || *in.Model)
	if err != nil {
		writeSortErr(w, err)
		return
	}
	apierr.WriteJSON(w, res)
}

// toolTodoSortSuggest is todo_sort suggest: the suggestions as a numbered list the assistant shows the user.
func (s *Service) toolTodoSortSuggest(ctx context.Context) (string, error) {
	res, err := s.suggestSort(ctx, s.lang(ctx), true)
	if err != nil {
		return "", err
	}
	if len(res.Items) == 0 {
		return fmt.Sprintf("Nothing to sort: no open to-do is outside the projects (%d are in one).", res.Sorted), nil
	}
	var w strings.Builder
	fmt.Fprintf(&w, "Suggestions for %d of the %d unsorted to-dos (nothing is moved yet):\n", len(res.Items), res.Unsorted)
	for i, it := range res.Items {
		switch {
		case it.Project != "":
			fmt.Fprintf(&w, "%d. %s %q → %s (%s) · %d%% sure · %s\n", i+1, it.Todo, it.Title, it.ProjectName, it.Project, int(math.Round(it.Confidence*100)), it.Reason)
		case it.Temporary:
			fmt.Fprintf(&w, "%d. %s %q → Temporary (a one-off) · %d%% sure · %s\n", i+1, it.Todo, it.Title, int(math.Round(it.Confidence*100)), it.Reason)
		default:
			fmt.Fprintf(&w, "%d. %s %q: no suggestion\n", i+1, it.Todo, it.Title)
		}
	}
	if res.Model == "unavailable" || res.Model == "failed" {
		w.WriteString("(No model could be asked: these are the rules' suggestions only.)\n")
	}
	w.WriteString("Show the user the list, the sure ones first, and file only what they accept, as they change it, with one todo_sort apply. " +
		"They can also review it on the To-dos page (Sort unsorted).")
	return w.String(), nil
}
