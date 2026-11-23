package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
)

// Token usage and its estimated cost per to-do (#12936, docs/assistant/todos.md "Usage and cost"). A to-do's usage is
// what every Agent session linked to it used (Todo.Agents, released ones too): the hub asks each computer for its
// sessions with one usage.sessions call (the node reads its own records, node/session_usage.go) and keeps the last
// answer per session, saved, so a computer that is offline still shows its last known numbers, marked stale. The rows
// keep the session, subagent, model and day, so the same data can later be summed per conversation, project or day.
//
// The cost is an estimate at public prices, never a bill: what a gateway actually charges is unknown. Each model is
// priced from the configured overrides, else the built-in catalog's reference price (models_catalog.json), else the
// default rate (Opus 5.5's public price unless configured).

const (
	todoUsagePricesKey   = "todo_usage_prices"
	todoUsageSessionsKey = "todo_usage_sessions"
	// todoUsageFresh: a session's numbers younger than this are not asked for again.
	todoUsageFresh = time.Minute
	// todoUsageWait bounds a read's calls to the computers; one that does not answer keeps its last numbers.
	todoUsageWait = 8 * time.Second
	// todoUsageListMax bounds the to-dos one list read sums.
	todoUsageListMax = 200
)

// UsagePrice is a rate in USD per million tokens of each kind.
type UsagePrice struct {
	Input      float64 `json:"input"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
	Output     float64 `json:"output"`
}

// UsagePrices is the configurable price table: the default rate and per-model overrides (by model id).
type UsagePrices struct {
	Default UsagePrice            `json:"default"`
	Models  map[string]UsagePrice `json:"models,omitempty"`
}

// defaultUsagePrice is Opus 5.5's public price: input $4, cache write $5, cache read $0.20, output $20 per million.
var defaultUsagePrice = UsagePrice{Input: 4, CacheWrite: 5, CacheRead: 0.2, Output: 20}

// UsageTokens are tokens in the four kinds that do not overlap, and their sum.
type UsageTokens struct {
	Input      int64 `json:"in"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
	Output     int64 `json:"out"`
	Total      int64 `json:"total"`
}

func (t *UsageTokens) add(r proto.SessionUsageRow) {
	t.Input += r.Input
	t.CacheWrite += r.CacheWrite
	t.CacheRead += r.CacheRead
	t.Output += r.Output
	t.Total = t.Input + t.CacheWrite + t.CacheRead + t.Output
}

// cost is what t costs at p, in USD.
func (t UsageTokens) cost(p UsagePrice) float64 {
	return (float64(t.Input)*p.Input + float64(t.CacheWrite)*p.CacheWrite + float64(t.CacheRead)*p.CacheRead +
		float64(t.Output)*p.Output) / 1e6
}

// TodoUsageSummary is a to-do's total, for its card.
type TodoUsageSummary struct {
	Tokens UsageTokens `json:"tokens"`
	USD    float64     `json:"usd"`
	// Stale: some of its sessions' numbers are older than this read (a computer did not answer).
	Stale bool `json:"stale,omitempty"`
	// Sessions counts the linked sessions; Found the ones a computer had records of.
	Sessions int `json:"sessions"`
	Found    int `json:"found"`
}

// TodoUsagePart is a session's or a subagent's share of a to-do's usage.
type TodoUsagePart struct {
	MachineID string      `json:"machine_id,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	Subagent  string      `json:"subagent,omitempty"`
	Title     string      `json:"title,omitempty"`
	Kind      string      `json:"kind,omitempty"` // a subagent's type
	Agent     string      `json:"agent,omitempty"`
	Found     bool        `json:"found"`
	Stale     bool        `json:"stale,omitempty"`
	UpdatedAt *time.Time  `json:"updated_at,omitempty"`
	Tokens    UsageTokens `json:"tokens"`
	USD       float64     `json:"usd"`
	// Subagents: a session's subagents, largest first (its own transcript's share is the session's minus theirs).
	Subagents []TodoUsagePart `json:"subagents,omitempty"`
}

// TodoUsageModel is a model's share and the price it was counted at.
type TodoUsageModel struct {
	Model  string      `json:"model"`
	Tokens UsageTokens `json:"tokens"`
	USD    float64     `json:"usd"`
	Price  UsagePrice  `json:"price"`
	// PriceFrom: "override", "catalog" or "default".
	PriceFrom string `json:"price_from"`
}

// TodoUsage is GET /api/v1/todos/{id}/usage.
type TodoUsage struct {
	TodoID string `json:"todo_id"`
	TodoUsageSummary
	Estimate bool             `json:"estimate"` // always true: public prices, not what a gateway bills
	Parts    []TodoUsagePart  `json:"parts"`
	Models   []TodoUsageModel `json:"models"`
}

// sessionUsageEntry is the last answer about one session.
type sessionUsageEntry struct {
	Found bool                    `json:"found"`
	Agent string                  `json:"agent,omitempty"`
	At    time.Time               `json:"at"`
	Rows  []proto.SessionUsageRow `json:"rows,omitempty"`
}

// todoUsageBook keeps the sessions' last answers ("<machine>/<session>"), loaded from the store once.
type todoUsageBook struct {
	mu       sync.Mutex
	loaded   bool
	sessions map[string]sessionUsageEntry
}

func todoUsageKey(machine, session string) string { return machine + "/" + session }

func (s *Service) todoUsageLoad(ctx context.Context) {
	b := &s.todoUsage
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.loaded {
		return
	}
	b.sessions = map[string]sessionUsageEntry{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, todoUsageSessionsKey, &b.sessions); err != nil {
			s.logf("todo usage: %v", err)
			return // not loaded: read again next time
		}
		if b.sessions == nil {
			b.sessions = map[string]sessionUsageEntry{}
		}
	}
	b.loaded = true
}

// usagePrices is the configured price table, the default when none was saved.
func (s *Service) usagePrices(ctx context.Context) UsagePrices {
	p := UsagePrices{Default: defaultUsagePrice}
	if st := s.store(); st != nil {
		var saved UsagePrices
		if ok, err := st.Setting(ctx, todoUsagePricesKey, &saved); err == nil && ok {
			if saved.Default != (UsagePrice{}) {
				p.Default = saved.Default
			}
			p.Models = saved.Models
		}
	}
	return p
}

// priceOf is the rate model is counted at, and where it came from.
func (p UsagePrices) priceOf(model string) (UsagePrice, string) {
	if m, ok := p.Models[model]; ok {
		return m, "override"
	}
	if c, ok := catalogModel(model); ok && c.InheritedFrom == "" && c.Price != nil {
		cp := c.Price
		price := UsagePrice{Input: cp.Input, Output: cp.Output, CacheWrite: cp.Input, CacheRead: cp.Input}
		if cp.CacheWrite != nil {
			price.CacheWrite = *cp.CacheWrite
		}
		if cp.CacheRead != nil {
			price.CacheRead = *cp.CacheRead
		}
		return price, "catalog"
	}
	return p.Default, "default"
}

func validUsagePrice(p UsagePrice) bool {
	for _, v := range []float64{p.Input, p.CacheWrite, p.CacheRead, p.Output} {
		if v < 0 || v > 10000 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// refreshSessionUsage asks each computer about its sessions whose numbers are older than todoUsageFresh, one call per
// computer, in parallel, and keeps what they answer. A computer that fails keeps its sessions' last numbers.
func (s *Service) refreshSessionUsage(ctx context.Context, agents []TodoAgent) {
	s.todoUsageLoad(ctx)
	now := time.Now()
	byMachine := map[string][]string{}
	s.todoUsage.mu.Lock()
	for _, a := range agents {
		if a.MachineID == "" || a.SessionID == "" {
			continue
		}
		e, ok := s.todoUsage.sessions[todoUsageKey(a.MachineID, a.SessionID)]
		if ok && now.Sub(e.At) < todoUsageFresh {
			continue
		}
		if !containsID(byMachine[a.MachineID], a.SessionID) {
			byMachine[a.MachineID] = append(byMachine[a.MachineID], a.SessionID)
		}
	}
	s.todoUsage.mu.Unlock()
	if len(byMachine) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, todoUsageWait)
	defer cancel()
	var wg sync.WaitGroup
	dirty := false
	for machine, ids := range byMachine {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out proto.SessionUsage
			if err := s.call(ctx, machine, proto.MethodSessionUsage, map[string]any{"session_ids": ids}, &out); err != nil {
				s.logf("todo usage: %s: %v", machine, err)
				return
			}
			rows := map[string][]proto.SessionUsageRow{}
			for _, r := range out.Rows {
				rows[r.SessionID] = append(rows[r.SessionID], r)
			}
			at := time.Now()
			s.todoUsage.mu.Lock()
			for _, f := range out.Sessions {
				if !containsID(ids, f.SessionID) {
					continue
				}
				s.todoUsage.sessions[todoUsageKey(machine, f.SessionID)] = sessionUsageEntry{Found: f.Found, Agent: f.Agent, At: at, Rows: rows[f.SessionID]}
			}
			dirty = true
			s.todoUsage.mu.Unlock()
		}()
	}
	wg.Wait()
	if dirty {
		if st := s.store(); st != nil {
			s.todoUsage.mu.Lock()
			snap := make(map[string]sessionUsageEntry, len(s.todoUsage.sessions))
			for k, v := range s.todoUsage.sessions {
				snap[k] = v
			}
			s.todoUsage.mu.Unlock()
			if err := st.PutSetting(context.WithoutCancel(ctx), todoUsageSessionsKey, snap); err != nil {
				s.logf("todo usage: save: %v", err)
			}
		}
	}
}

func containsID(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// todoUsageOf sums the kept numbers of todo's sessions (refreshSessionUsage first for fresh ones).
func (s *Service) todoUsageOf(todo Todo, prices UsagePrices, detail bool) TodoUsage {
	out := TodoUsage{TodoID: todo.ID, Estimate: true, Parts: []TodoUsagePart{}, Models: []TodoUsageModel{}}
	now := time.Now()
	models := map[string]*TodoUsageModel{}
	seen := map[string]bool{}
	s.todoUsage.mu.Lock()
	defer s.todoUsage.mu.Unlock()
	for _, a := range todo.Agents {
		key := todoUsageKey(a.MachineID, a.SessionID)
		if a.MachineID == "" || a.SessionID == "" || seen[key] {
			continue
		}
		seen[key] = true
		out.Sessions++
		e, ok := s.todoUsage.sessions[key]
		part := TodoUsagePart{MachineID: a.MachineID, SessionID: a.SessionID, Title: a.Title, Agent: a.Agent, Found: e.Found}
		if e.Agent != "" {
			part.Agent = e.Agent
		}
		if ok {
			at := e.At
			part.UpdatedAt = &at
			part.Stale = now.Sub(e.At) > 2*todoUsageFresh
		} else {
			part.Stale = true
		}
		if part.Stale {
			out.Stale = true
		}
		if e.Found {
			out.Found++
		}
		subs := map[string]*TodoUsagePart{}
		for _, r := range e.Rows {
			price, from := prices.priceOf(r.Model)
			var t UsageTokens
			t.add(r)
			usd := t.cost(price)
			out.Tokens.add(r)
			out.USD += usd
			part.Tokens.add(r)
			part.USD += usd
			m := models[r.Model]
			if m == nil {
				m = &TodoUsageModel{Model: r.Model, Price: price, PriceFrom: from}
				models[r.Model] = m
			}
			m.Tokens.add(r)
			m.USD += usd
			if r.Subagent != "" {
				sp := subs[r.Subagent]
				if sp == nil {
					sp = &TodoUsagePart{Subagent: r.Subagent, Title: r.SubagentTitle, Kind: r.SubagentType, Agent: r.Agent, Found: true}
					subs[r.Subagent] = sp
				}
				sp.Tokens.add(r)
				sp.USD += usd
			}
		}
		if detail {
			for _, sp := range subs {
				part.Subagents = append(part.Subagents, *sp)
			}
			sort.Slice(part.Subagents, func(i, j int) bool { return part.Subagents[i].Tokens.Total > part.Subagents[j].Tokens.Total })
			out.Parts = append(out.Parts, part)
		}
	}
	if detail {
		for _, m := range models {
			out.Models = append(out.Models, *m)
		}
		sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].Tokens.Total > out.Models[j].Tokens.Total })
	}
	return out
}

func (s *Service) todoUsageRoutes() {
	// GET /api/v1/todos/usage?ids=a,b: each to-do's total and estimated USD (its card); unknown ids are left out.
	s.handle("GET /api/v1/todos/usage", func(w http.ResponseWriter, r *http.Request) {
		var todos []Todo
		var agents []TodoAgent
		for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
			if id = strings.TrimSpace(id); id == "" || len(todos) >= todoUsageListMax {
				continue
			}
			if t, ok := s.todoNow(id); ok {
				todos = append(todos, t)
				agents = append(agents, t.Agents...)
			}
		}
		s.refreshSessionUsage(r.Context(), agents)
		prices := s.usagePrices(r.Context())
		out := map[string]TodoUsageSummary{}
		for _, t := range todos {
			out[t.ID] = s.todoUsageOf(t, prices, false).TodoUsageSummary
		}
		apierr.WriteJSON(w, map[string]any{"todos": out, "estimate": true})
	})
	// GET /api/v1/todos/{id}/usage: the to-do's total, estimated USD, by session and subagent, and by model.
	s.handle("GET /api/v1/todos/{id}/usage", func(w http.ResponseWriter, r *http.Request) {
		t, ok := s.todoNow(r.PathValue("id"))
		if !ok {
			apierr.Write(w, http.StatusNotFound, errors.New("no such to-do"))
			return
		}
		s.refreshSessionUsage(r.Context(), t.Agents)
		apierr.WriteJSON(w, s.todoUsageOf(t, s.usagePrices(r.Context()), true))
	})
	// GET /api/v1/todos/usage/prices: the price table (USD per million tokens) and the built-in default.
	s.handle("GET /api/v1/todos/usage/prices", func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteJSON(w, map[string]any{"prices": s.usagePrices(r.Context()), "builtin_default": defaultUsagePrice})
	})
	// PUT /api/v1/todos/usage/prices {default?, models?}: replaces the table; an empty default goes back to the
	// built-in one.
	s.handle("PUT /api/v1/todos/usage/prices", func(w http.ResponseWriter, r *http.Request) {
		var in UsagePrices
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		ok := validUsagePrice(in.Default) && len(in.Models) <= 200
		for id, p := range in.Models {
			ok = ok && strings.TrimSpace(id) != "" && len(id) <= 200 && validUsagePrice(p)
		}
		if !ok {
			apierr.Write(w, http.StatusBadRequest, errors.New("prices must be between 0 and 10000 USD per million tokens"))
			return
		}
		if err := s.putSetting(r.Context(), todoUsagePricesKey, in); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"prices": s.usagePrices(r.Context()), "builtin_default": defaultUsagePrice})
	})
}
