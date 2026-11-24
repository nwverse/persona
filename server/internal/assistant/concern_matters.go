package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Matters (docs/assistant/todos.md "Matters"): the owner thinks in matters, things with a clear edge they want an answer
// or a result for, not in conversations or tasks. A matter is a concern (concerns.go): a to-do of kind concern. It holds
//
//   - the owner's words about it: every message the owner writes in the main conversation is filed under exactly one
//     concern, an existing one or a new one, by the main conversation as it handles the message (concern_file; making a
//     concern with todo_create files the message being handled too), or by the owner moving it in the app
//     (PUT /api/v1/todos/{id}/words);
//   - what it is (about): a few plain sentences, what the matter is, why it was raised and what counts as done, so a
//     matter with a short title stays readable later; written with concern_file, best when the matter is made;
//   - where it stands: the main conversation's line of what is being done or waited for (now) and, when the owner must
//     decide something, the question and its answers (ask), both written with concern_file;
//   - its tasks: the to-dos whose part_of is the concern (or whose part_of is a request root whose part_of is the
//     concern), else the tasks handed out for one of its messages (the brief's request). GET /api/v1/todos gives each
//     to-do its `matter`.
//
// What the apps show of it follows header_card (header_card.go): a matter that was reported with concern_report and
// not confirmed since, or that waits for the owner's decision, raises a card at the top; a task never does. 知道了 is
// PUT /api/v1/todos/{id}/confirm, which for an open concern reports what the card said first.

// concernWordMax is how much of a message a concern keeps of it; the whole of it stays in the conversation.
const concernWordMax = 2000

// concernAboutMax is how much of a concern's about it keeps.
const concernAboutMax = 1000

// concernFileTurns is how far back concern_file looks for the owner's messages not filed yet.
const concernFileTurns = 6

// ConcernWord is a message of the owner's filed under a concern: what they wrote, when, and where (the conversation
// and the message's id in its transcript). By is "owner" when they moved it there in the app.
type ConcernWord struct {
	Text         string    `json:"text"`
	At           time.Time `json:"at"`
	Conversation string    `json:"conversation"`
	MessageID    string    `json:"message_id"`
	By           string    `json:"by,omitempty"`
	todo         string
	filedAt      time.Time
}

// ConcernView is what GET /api/v1/todos carries on a concern (`concern`): its words, oldest first, what it is about,
// where it stands, and the directions its tasks are supervised by (none: the main conversation or the background
// supervisor follows them).
type ConcernView struct {
	Words      []ConcernWord      `json:"words"`
	About      string             `json:"about,omitempty"`
	Directions []ConcernDirection `json:"directions,omitempty"`
	Now        string             `json:"now,omitempty"`
	Ask        string             `json:"ask,omitempty"`
	Options    []string           `json:"options,omitempty"`
	UpdatedAt  *time.Time         `json:"updated_at,omitempty"`
}

// ConcernDirection is a direction (a leader conversation) supervising a task of a concern: the conversation's id and
// what the direction is called (directionName).
type ConcernDirection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wordKey struct{ conversation, message string }

// concernBook is the filed words and the concerns' states, read from the store once.
type concernBook struct {
	mu     sync.Mutex
	loaded bool
	words  map[wordKey]ConcernWord
	state  map[string]db.ConcernStateRow
}

// concernStore keeps them (the hub's database); without one they live in memory.
type concernStore interface {
	ListConcernWords(ctx context.Context) ([]db.ConcernWordRow, error)
	PutConcernWord(ctx context.Context, r db.ConcernWordRow) error
	ListConcernStates(ctx context.Context) ([]db.ConcernStateRow, error)
	PutConcernState(ctx context.Context, r db.ConcernStateRow) error
}

var _ concernStore = (*db.DB)(nil)

func (s *Service) concernDB() concernStore {
	st, _ := s.store().(concernStore)
	return st
}

// concernsLocked reads the book once; b.mu is held. A failed read leaves it unloaded.
func (s *Service) concernsLocked(ctx context.Context) error {
	b := &s.concerns
	if b.loaded {
		return nil
	}
	words, state := map[wordKey]ConcernWord{}, map[string]db.ConcernStateRow{}
	if st := s.concernDB(); st != nil {
		rows, err := st.ListConcernWords(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			by := ""
			if r.FiledBy == db.FiledByOwner {
				by = db.FiledByOwner
			}
			words[wordKey{r.Conversation, r.MessageID}] = ConcernWord{Text: r.Text, At: r.SaidAt, Conversation: r.Conversation,
				MessageID: r.MessageID, By: by, todo: r.TodoID, filedAt: r.FiledAt}
		}
		states, err := st.ListConcernStates(ctx)
		if err != nil {
			return err
		}
		for _, r := range states {
			state[r.TodoID] = r
		}
	}
	b.words, b.state, b.loaded = words, state, true
	return nil
}

// concernViews are the words and states by concern id, for the to-dos GET returns, and the concern each filed message
// is under.
func (s *Service) concernViews(ctx context.Context) (map[string]*ConcernView, map[wordKey]string) {
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	views, filed := map[string]*ConcernView{}, map[wordKey]string{}
	if err := s.concernsLocked(ctx); err != nil {
		s.logf("concerns: read: %v", err)
		return views, filed
	}
	view := func(id string) *ConcernView {
		v := views[id]
		if v == nil {
			v = &ConcernView{Words: []ConcernWord{}}
			views[id] = v
		}
		return v
	}
	for k, w := range b.words {
		filed[k] = w.todo
		v := view(w.todo)
		v.Words = append(v.Words, w)
	}
	for id, st := range b.state {
		v := view(id)
		v.About, v.Now, v.Ask = st.About, st.Now, st.Ask
		if len(st.Options) > 0 {
			v.Options = append([]string(nil), st.Options...)
		}
		at := st.UpdatedAt
		v.UpdatedAt = &at
	}
	for _, v := range views {
		sort.Slice(v.Words, func(i, j int) bool {
			if !v.Words[i].At.Equal(v.Words[j].At) {
				return v.Words[i].At.Before(v.Words[j].At)
			}
			return v.Words[i].MessageID < v.Words[j].MessageID
		})
	}
	return views, filed
}

// fileWord files message w under concern id (moving it from the concern it was under), by db.FiledByAssistant or
// db.FiledByOwner. db.ErrTodoNotFound when there is no such concern.
func (s *Service) fileWord(ctx context.Context, id string, w ConcernWord, by string) error {
	if t, ok := s.todoNow(id); !ok || !t.isConcern() {
		return db.ErrTodoNotFound
	}
	if w.Conversation == "" || w.MessageID == "" {
		return errors.New("a message to file needs its conversation and id")
	}
	if utf8.RuneCountInString(w.Text) > concernWordMax {
		w.Text = string([]rune(w.Text)[:concernWordMax])
	}
	now := time.Now().UTC()
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.concernsLocked(ctx); err != nil {
		return err
	}
	k := wordKey{w.Conversation, w.MessageID}
	if cur, ok := b.words[k]; ok {
		w.Text, w.At = cur.Text, cur.At // the message is what it was: only where it is filed changes
	}
	if st := s.concernDB(); st != nil {
		if err := st.PutConcernWord(ctx, db.ConcernWordRow{Conversation: w.Conversation, MessageID: w.MessageID, TodoID: id,
			Text: w.Text, SaidAt: w.At.UTC(), FiledAt: now, FiledBy: by}); err != nil {
			return err
		}
	}
	w.todo, w.filedAt, w.By = id, now, ""
	if by == db.FiledByOwner {
		w.By = db.FiledByOwner
	}
	b.words[k] = w
	return nil
}

// filedUnder is the concern message k is filed under ("" for none).
func (s *Service) filedUnder(ctx context.Context, k wordKey) string {
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.concernsLocked(ctx) != nil {
		return ""
	}
	return b.words[k].todo
}

// setConcernState writes concern id's now line and ask (nil: as it is; "" clears it; options go with ask).
func (s *Service) setConcernState(ctx context.Context, id string, now, ask *string, options []string) error {
	if now == nil && ask == nil {
		return nil
	}
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.concernsLocked(ctx); err != nil {
		return err
	}
	r := b.state[id]
	r.TodoID, r.UpdatedAt = id, time.Now().UTC()
	if now != nil {
		r.Now = clip(oneLine(strings.TrimSpace(*now)), 300)
	}
	if ask != nil {
		r.Ask = clip(strings.TrimSpace(*ask), 600)
		r.Options = nil
		if r.Ask != "" {
			for _, o := range options {
				if o = clip(oneLine(strings.TrimSpace(o)), 60); o != "" && len(r.Options) < 4 {
					r.Options = append(r.Options, o)
				}
			}
		}
	}
	if st := s.concernDB(); st != nil {
		if err := st.PutConcernState(ctx, r); err != nil {
			return err
		}
	}
	b.state[id] = r
	return nil
}

// setConcernAbout writes what concern id is about ("" clears it).
func (s *Service) setConcernAbout(ctx context.Context, id, about string) error {
	about = strings.TrimSpace(about)
	if utf8.RuneCountInString(about) > concernAboutMax {
		about = string([]rune(about)[:concernAboutMax])
	}
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.concernsLocked(ctx); err != nil {
		return err
	}
	r := b.state[id]
	r.TodoID, r.About, r.UpdatedAt = id, about, time.Now().UTC()
	if st := s.concernDB(); st != nil {
		if err := st.PutConcernState(ctx, r); err != nil {
			return err
		}
	}
	b.state[id] = r
	return nil
}

// concernAbout is what concern id is about ("" none, or unreadable).
func (s *Service) concernAbout(ctx context.Context, id string) string {
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.concernsLocked(ctx) != nil {
		return ""
	}
	return b.state[id].About
}

// TaskFollow is who follows a task, as GET /api/v1/todos carries it on each task (`followed_by`) for the top of its
// page: a direction (by "direction": a leader conversation, a current leader or one with a direction record, ended
// too, with its name), the main conversation itself ("main"), the background supervisor ("background"), or another
// conversation ("other"). A task nobody supervises carries none.
type TaskFollow struct {
	By        string            `json:"by"`
	Direction *ConcernDirection `json:"direction,omitempty"`
}

// taskFollows is who follows each supervised task, by to-do id (TaskFollow); directions are named as elsewhere
// (leaderNames).
func (s *Service) taskFollows(ctx context.Context, tasks []Todo) map[string]TaskFollow {
	cfg := s.settings(ctx)
	isDirection := map[string]bool{}
	out := map[string]TaskFollow{}
	var all []string
	for _, t := range tasks {
		if t.Supervision == nil || t.Supervision.Supervisor == "" {
			continue
		}
		sup := t.Supervision.Supervisor
		switch {
		case sup == cfg.Main:
			out[t.ID] = TaskFollow{By: "main"}
			continue
		case sup == cfg.BackgroundSupervisor:
			out[t.ID] = TaskFollow{By: "background"}
			continue
		}
		is, known := isDirection[sup]
		if !known {
			is = slices.Contains(cfg.Leaders, sup) || s.directionOf(sup) != nil
			isDirection[sup] = is
			if is {
				all = append(all, sup)
			}
		}
		if !is {
			out[t.ID] = TaskFollow{By: "other"}
			continue
		}
		out[t.ID] = TaskFollow{By: "direction", Direction: &ConcernDirection{ID: sup}}
	}
	if len(all) == 0 {
		return out
	}
	names := s.leaderNames(ctx, all)
	for id, f := range out {
		if f.Direction != nil {
			f.Direction.Name = names[f.Direction.ID]
			out[id] = f
		}
	}
	return out
}

// concernDirections are the directions supervising the tasks of each concern, by concern id, from who follows each
// task (follows, taskFollows): the leader conversations named in the tasks' supervision.supervisor, ordered by name.
// The main conversation and the background supervisor are no direction. matter is each task's concern ("" none).
func concernDirections(tasks []Todo, follows map[string]TaskFollow, matter func(Todo) string) map[string][]ConcernDirection {
	sups := map[string]map[string]string{} // concern → its directions → their names
	for _, t := range tasks {
		c, f := matter(t), follows[t.ID]
		if c == "" || f.Direction == nil {
			continue
		}
		if sups[c] == nil {
			sups[c] = map[string]string{}
		}
		sups[c][f.Direction.ID] = f.Direction.Name
	}
	out := make(map[string][]ConcernDirection, len(sups))
	for c, set := range sups {
		for id, name := range set {
			out[c] = append(out[c], ConcernDirection{ID: id, Name: name})
		}
		slices.SortFunc(out[c], func(a, b ConcernDirection) int {
			return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
		})
	}
	return out
}

// ownerMessages are the owner's own messages among the last turns of conversation on machine, oldest first: what they
// wrote, never what Persona or another conversation put there (briefOrigin's reading of a message).
func (s *Service) ownerMessages(ctx context.Context, machine, conversation string, turns int) ([]BriefRequest, error) {
	var res struct {
		Items []proto.Item `json:"items"`
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": conversation, "turns": turns, "lazy_images": true}, &res); err != nil {
		return nil, err
	}
	var out []BriefRequest
	for _, it := range res.Items {
		if text, ok := ownerText(it); ok {
			out = append(out, BriefRequest{Text: text, At: it.TS.UTC(), Conversation: conversation, MessageID: it.ID})
		}
	}
	return out, nil
}

// ownerText is a message's text when the owner wrote it (not a tool result, a picture, a notice, a message Persona
// typed in for them or one relayed from elsewhere), without the context blocks around it; false for any other item.
// Unlike ownerMessageText (dispatch_policy.go) it takes a message without a client message id: one typed in a terminal
// or read through an older node is the owner's too, and every owner message is filed. Persona's own notices, which
// have none either, are told by their text (prompts.ParseInjected).
func ownerText(it proto.Item) (string, bool) {
	if it.Role != "user" || it.Kind != proto.KindText || it.Task != nil && !it.Wake || it.ID == "" {
		return "", false
	}
	if prompts.ItemSource(it) != nil || it.From != "" || it.Wake || it.Injected != nil || prompts.ParseInjected(it.Text) != nil {
		return "", false
	}
	text := strings.TrimSpace(stripContext(it.Text))
	return text, text != ""
}

// pickOwnerMessage is the message concern_file files: the newest of msgs whose text holds quote (any when quote is
// empty) and that is not filed yet, or, with quote, filed already (to move it). ok is false when there is none.
func (s *Service) pickOwnerMessage(ctx context.Context, msgs []BriefRequest, quote string) (BriefRequest, bool) {
	q := strings.ToLower(strings.Join(strings.Fields(quote), " "))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if q != "" {
			if strings.Contains(strings.ToLower(strings.Join(strings.Fields(m.Text), " ")), q) {
				return m, true
			}
			continue
		}
		if s.filedUnder(ctx, wordKey{m.Conversation, m.MessageID}) == "" {
			return m, true
		}
	}
	return BriefRequest{}, false
}

// fileCurrentMessage files the owner's newest message not filed yet in conversation self under concern id (todo_create
// of a concern): what the main conversation is handling now. Best effort: a conversation that can't be read files
// nothing. It says what it filed, "" for nothing.
func (s *Service) fileCurrentMessage(ctx context.Context, machine, self, id string) string {
	msgs, err := s.ownerMessages(ctx, machine, self, concernFileTurns)
	if err != nil {
		s.logf("concerns: read %s: %v", short(self), err)
		return ""
	}
	m, ok := s.pickOwnerMessage(ctx, msgs, "")
	if !ok {
		return ""
	}
	if err := s.fileWord(ctx, id, ConcernWord{Text: m.Text, At: m.At, Conversation: m.Conversation, MessageID: m.MessageID}, db.FiledByAssistant); err != nil {
		s.logf("concerns: file %s under %s: %v", m.MessageID, id, err)
		return ""
	}
	return m.Text
}

// toolConcernFile runs concern_file for conversation self, the main conversation: it files the owner's message it is
// handling under a concern (id, or a new one titled title), and writes where that concern stands (now, ask).
func (s *Service) toolConcernFile(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	if !s.isMain(ctx, self) {
		return "", errConcernMain
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", fmt.Errorf("the todos can't be read now (%v): try again later", err)
	}
	var a struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Notes   string   `json:"notes"`
		Quote   string   `json:"quote"`
		Message string   `json:"message"`
		About   *string  `json:"about"`
		Now     *string  `json:"now"`
		Ask     *string  `json:"ask"`
		Options []string `json:"options"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	id, title := strings.TrimSpace(a.ID), strings.TrimSpace(a.Title)
	var lines []string
	switch {
	case id != "":
		t, ok := s.todoNow(id)
		if !ok {
			return "", fmt.Errorf("no to-do %q: todo_list kind concern has the concerns", id)
		}
		if !t.isConcern() {
			if t.PartOf != "" {
				if root, ok := s.todoNow(t.PartOf); ok && root.isConcern() {
					return "", fmt.Errorf("%s is a task of concern %s: file under %s", id, root.ID, root.ID)
				}
			}
			return "", fmt.Errorf("%s is not a concern: give a concern's id, or a title for a new one", id)
		}
	case title != "":
		if t, ok := s.openTodoLike(title, db.KindConcern, db.TodoEdit{}); ok && t.isConcern() {
			id = t.ID
			break
		}
		t, err := s.createConcern(ctx, title, strings.TrimSpace(a.Notes), db.TodoEdit{})
		if err != nil {
			return "", fmt.Errorf("the concern could not be saved, nothing filed: %w", err)
		}
		id = t.ID
		lines = append(lines, fmt.Sprintf("New concern %s %q.", id, t.Title))
	default:
		return "", errors.New("give id, an existing concern (todo_list kind concern), or title, a new one")
	}

	if strings.TrimSpace(a.Message) != "none" {
		msgs, err := s.ownerMessages(ctx, machine, self, concernFileTurns)
		if err != nil {
			lines = append(lines, "The conversation could not be read, no message filed ("+err.Error()+").")
		} else if m, ok := s.pickOwnerMessage(ctx, msgs, a.Quote); !ok {
			if strings.TrimSpace(a.Quote) != "" {
				lines = append(lines, "No message of the owner's in the last turns holds that quote: nothing filed.")
			} else {
				lines = append(lines, "The owner's recent messages are all filed already: nothing new filed (give quote to move one).")
			}
		} else {
			from := s.filedUnder(ctx, wordKey{m.Conversation, m.MessageID})
			if err := s.fileWord(ctx, id, ConcernWord{Text: m.Text, At: m.At, Conversation: m.Conversation, MessageID: m.MessageID}, db.FiledByAssistant); err != nil {
				return "", fmt.Errorf("the message could not be filed: %w", err)
			}
			what := "Filed"
			if from != "" && from != id {
				what = "Moved from " + from + " and filed"
			}
			lines = append(lines, fmt.Sprintf("%s %q under %s.", what, clip(oneLine(m.Text), 60), id))
			if t, ok := s.todoNow(id); ok && t.DoneAt != nil {
				// the owner speaks of a matter that was done: it is being worked on again
				if err := s.reopenTodo(ctx, id); err != nil {
					lines = append(lines, "It was done and could not be reopened ("+err.Error()+"): call concern_file again to reopen it.")
				} else {
					lines = append(lines, "It was done, so it is open again: report its new outcome with concern_report.")
				}
			}
		}
	}
	if a.About != nil {
		if err := s.setConcernAbout(ctx, id, *a.About); err != nil {
			return "", fmt.Errorf("what it is about could not be saved: %w", err)
		}
		if strings.TrimSpace(*a.About) == "" {
			lines = append(lines, "Its about is cleared.")
		} else {
			lines = append(lines, "Its about is saved: the owner reads it at the top of the matter's page.")
		}
	} else if s.concernAbout(ctx, id) == "" {
		lines = append(lines, "It has no about yet: give about (what it is, why, what counts as done) so the owner can read it later.")
	}
	if err := s.setConcernState(ctx, id, a.Now, a.Ask, a.Options); err != nil {
		return "", fmt.Errorf("where it stands could not be saved: %w", err)
	}
	if a.Ask != nil && strings.TrimSpace(*a.Ask) != "" {
		lines = append(lines, "The owner sees the question at the top with its answers; their answer comes back as a message.")
	}
	s.todosChanged(ctx)
	if t, ok := s.todoNow(id); ok {
		lines = append(lines, todoLine(t, time.Now(), s.location(ctx)))
	}
	return strings.Join(lines, "\n"), nil
}

// clearConcernAsk takes concern id's question away: its outcome was reported, so nothing waits for the owner's answer.
func (s *Service) clearConcernAsk(ctx context.Context, id string) {
	empty := ""
	b := &s.concerns
	b.mu.Lock()
	_, has := b.state[id]
	asking := has && b.state[id].Ask != ""
	b.mu.Unlock()
	if !asking {
		return
	}
	if err := s.setConcernState(ctx, id, nil, &empty, nil); err != nil {
		s.logf("concern %s: clear its ask: %v", id, err)
	}
}

// matterOf is the concern to-do t belongs to ("" for none): the first concern up its part_of chain, else the concern
// the owner's message its brief started from is filed under.
func matterOf(t Todo, byID map[string]Todo, filed map[wordKey]string) string {
	if t.isConcern() {
		return ""
	}
	// up the part_of chain: a piece of a request is two hops away, and a reorganized goal tree can be deeper
	seen := map[string]bool{t.ID: true}
	for id := t.PartOf; id != "" && !seen[id]; {
		seen[id] = true
		up, ok := byID[id]
		if !ok {
			break
		}
		if up.isConcern() {
			return up.ID
		}
		id = up.PartOf
	}
	if t.Brief != nil && t.Brief.Request != nil {
		if c := filed[wordKey{t.Brief.Request.Conversation, t.Brief.Request.MessageID}]; c != "" {
			if root, ok := byID[c]; ok && root.isConcern() {
				return c
			}
		}
	}
	return ""
}

// matterTasks are the to-dos of concern id (matterOf), as the book has them now.
func (s *Service) matterTasks(ctx context.Context, id string) []Todo {
	_, filed := s.concernViews(ctx)
	links := s.briefLinksNow()
	s.todos.mu.RLock()
	byID := make(map[string]Todo, len(s.todos.todos))
	for _, t := range s.todos.todos {
		byID[t.ID] = copyTodoLocked(t)
	}
	s.todos.mu.RUnlock()
	var out []Todo
	for _, t := range byID {
		if t.Brief == nil {
			t = s.withBriefFrom(t, links)
		}
		if matterOf(t, byID, filed) == id {
			out = append(out, t)
		}
	}
	return out
}

// confirmMatter is 知道了 on concern id: an open one is reported first, with report, the conclusion its card showed
// (an open concern can't be put away without one); then it is confirmed, and so is each finished task of it, so none of
// them waits for the owner any more.
func (s *Service) confirmMatter(ctx context.Context, id, report string) (Todo, error) {
	t, ok := s.todoNow(id)
	if !ok {
		return Todo{}, db.ErrTodoNotFound
	}
	if t.DoneAt == nil {
		if strings.TrimSpace(report) == "" {
			return Todo{}, errEmptyReport
		}
		// reported and confirmed in one write: the owner pressed Got it on that very conclusion
		r, _, err := s.reportConcern(ctx, id, report, "", "", true)
		if err != nil {
			return Todo{}, err
		}
		s.clearConcernAsk(ctx, id)
		t = r
	} else {
		c, err := s.confirmFinished(ctx, id, true)
		if err != nil {
			return Todo{}, err
		}
		t = c
	}
	out := t
	for _, task := range s.matterTasks(ctx, id) {
		if task.DoneAt != nil && !confirmedSinceReport(task) {
			if _, err := s.confirmFinished(ctx, task.ID, true); err != nil {
				s.logf("concern %s: confirm its task %s: %v", id, task.ID, err)
				continue
			}
			s.answerAskOnConfirm(ctx, task.ID) // nothing of the matter is left waiting on a client
		}
	}
	return out, nil
}

func (s *Service) concernWordRoutes() {
	// PUT /api/v1/todos/{id}/words {conversation, message_id, text?, at?}: the owner files one of their messages under
	// concern id, moving it from the one it was under (text and at name a message not filed before).
	s.handle("PUT /api/v1/todos/{id}/words", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
			return
		}
		if err := s.ensureTodosLoaded(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
			return
		}
		var body struct {
			Conversation string    `json:"conversation"`
			MessageID    string    `json:"message_id"`
			Text         string    `json:"text"`
			At           time.Time `json:"at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		id := r.PathValue("id")
		k := wordKey{strings.TrimSpace(body.Conversation), strings.TrimSpace(body.MessageID)}
		if k.conversation == "" || k.message == "" {
			apierr.Write(w, http.StatusBadRequest, errors.New("conversation and message_id are required"))
			return
		}
		if s.filedUnder(r.Context(), k) == "" && strings.TrimSpace(body.Text) == "" {
			apierr.Write(w, http.StatusBadRequest, errors.New("a message not filed before needs its text"))
			return
		}
		at := body.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		err := s.fileWord(r.Context(), id, ConcernWord{Text: strings.TrimSpace(body.Text), At: at, Conversation: k.conversation, MessageID: k.message}, db.FiledByOwner)
		if errors.Is(err, db.ErrTodoNotFound) {
			apierr.Write(w, http.StatusNotFound, errors.New("no such concern"))
			return
		}
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		if t, ok := s.todoNow(id); ok && t.DoneAt != nil {
			// as concern_file: the owner speaks of a matter that was done, so it is being worked on again
			if err := s.reopenTodo(r.Context(), id); err != nil {
				apierr.Write(w, http.StatusInternalServerError, fmt.Errorf("the message is filed, but the concern could not be reopened: %w", err))
				return
			}
		}
		s.todosChanged(r.Context())
		t, _ := s.todoNow(id)
		apierr.WriteJSON(w, map[string]any{"todo": t})
	})
}

// enrichMatters gives every row of list its matter fields, as GET /api/v1/todos and GET /api/v1/todos/{id} carry
// them: a concern its words and where it stands (`concern`, with the directions supervising its tasks), a task the
// concern it belongs to (`matter`) and who follows it, and each row its header card (header_card.go). The whole list
// is read together: matterOf follows part_of links and filed messages across the list.
func (s *Service) enrichMatters(ctx context.Context, list []listedTodo) {
	views, filed := s.concernViews(ctx)
	byID := make(map[string]Todo, len(list))
	for i := range list {
		byID[list[i].ID] = list[i].Todo
	}
	tasks := make([]Todo, 0, len(list))
	matter := map[string]string{}
	for i := range list {
		if list[i].isConcern() {
			v := views[list[i].ID]
			if v == nil {
				v = &ConcernView{Words: []ConcernWord{}}
			}
			list[i].Concern = v
			continue
		}
		list[i].Matter = matterOf(list[i].Todo, byID, filed)
		matter[list[i].ID] = list[i].Matter
		tasks = append(tasks, list[i].Todo)
	}
	follows := s.taskFollows(ctx, tasks) // who follows each task
	dirs := concernDirections(tasks, follows, func(t Todo) string { return matter[t.ID] })
	for i := range list {
		if list[i].Concern != nil {
			list[i].Concern.Directions = dirs[list[i].ID]
			concernAsk := ""
			if v := views[list[i].ID]; v != nil {
				concernAsk = v.Ask
			}
			list[i].HeaderCard = headerCard(list[i].Todo, list[i].Ask, concernAsk)
			continue
		}
		if f, ok := follows[list[i].ID]; ok {
			list[i].FollowedBy = &f
		}
		list[i].HeaderCard = headerCard(list[i].Todo, list[i].Ask, "")
	}
}

// forgetConcern drops deleted to-do id's filed words and state from the book; the store cascades them itself.
func (s *Service) forgetConcern(id string) {
	b := &s.concerns
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, id)
	for k, w := range b.words {
		if w.todo == id {
			delete(b.words, k)
		}
	}
}
