package assistant

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	maxAutomaticParks = 3
	sigParkedSummary  = "parked_summary"
)

type automaticPark struct {
	Dependency string    `json:"dependency"`
	Turn       string    `json:"turn"`
	Count      int       `json:"count"`
	FirstAt    time.Time `json:"first_at"`
	SummaryDay string    `json:"summary_day,omitempty"`
	SummaryTo  string    `json:"summary_to,omitempty"`
}

var closeoutDate = lazyre.New(`\b\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2}))?\b`)
var closeoutSource = lazyre.New(`https?://[^\s<>"]+`)

// The optional park line uses task_review's existing evidence contract, not another waiting schema.
func closeoutPark(reply string, previous *TaskReview, now time.Time, loc *time.Location) (Parked, bool) {
	c, artifacts, ok := closeout.Parse(reply)
	if !ok || c.Status != proto.CloseoutBlocked || c.Waiting == "" || closeoutDisproved(c) {
		return Parked{}, false
	}
	// i18n-ignore: literal Agent closeout inputs; an owner's decision must stay actionable.
	if containsAny(strings.ToLower(c.Waiting), []string{"owner decision", "user decision", "user approval", "用户决定", "用户批准", "用户拍板"}) {
		return Parked{}, false
	}
	start := strings.LastIndex(reply, "<closeout")
	body, _, complete := strings.Cut(reply[start:], "</closeout>")
	if !complete {
		return Parked{}, false
	}
	var input struct {
		WaitingOn string        `json:"waiting_on"`
		Until     string        `json:"until"`
		Evidence  *ParkEvidence `json:"evidence"`
	}
	explicit := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*"))
		key, value, found := strings.Cut(line, ":")
		if !found || key != "park" {
			continue
		}
		if explicit || json.Unmarshal([]byte(value), &input) != nil {
			return Parked{}, false
		}
		explicit = true
	}
	if explicit && (input.Evidence == nil || !strings.Contains(strings.ToLower(c.Waiting),
		strings.ToLower(strings.TrimSpace(input.Evidence.Target)))) {
		return Parked{}, false
	}
	if !explicit {
		// Reuse an inspected receipt only when this closeout explicitly names the same target and source.
		if previous != nil && previous.Evidence != nil && previous.Reply == nil && previous.Decided == "" &&
			closeoutBinds(c, artifacts, previous.Evidence) {
			input.WaitingOn, input.Evidence = previous.WaitingOn, previous.Evidence.clone()
			if input.Evidence.Kind == "date" {
				input.Until = previous.Until.Format(time.RFC3339)
			}
		} else if date := closeoutDate.FindString(c.Waiting); date != "" {
			at, err := time.Parse(time.RFC3339, date)
			if len(date) == len(time.DateOnly) {
				at, err = time.ParseInLocation(time.DateOnly, date, loc)
			}
			if err != nil {
				return Parked{}, false
			}
			input.WaitingOn, input.Until = parkDate, at.Format(time.RFC3339)
			input.Evidence = &ParkEvidence{Kind: "date", Target: c.Waiting, Wake: c.Waiting}
		} else if externalCloseout(c.Waiting) {
			source := strings.TrimRight(closeoutSource.FindString(c.Waiting), ".,;)")
			if source == "" {
				for _, a := range artifacts {
					if a.Kind != "build" && a.Kind != "url" {
						continue
					}
					u, err := url.Parse(a.Ref)
					if err != nil {
						continue
					}
					name := strings.Trim(strings.TrimSuffix(u.Path, "/"), "/")
					if i := strings.LastIndex(name, "/"); i >= 0 {
						name = name[i+1:]
					}
					if len(name) >= 3 && strings.Contains(c.Waiting, name) && parkSourceURL(a.Ref) {
						if source != "" && source != a.Ref {
							return Parked{}, false
						}
						source = a.Ref
					}
				}
			}
			if source == "" || parkReviewURL(source) {
				return Parked{}, false // review/contact evidence must not be invented from a PR link
			}
			input.WaitingOn = parkExternalEvent
			input.Evidence = &ParkEvidence{Kind: "external_event", Target: c.Waiting, URL: source, Wake: c.Waiting}
		}
	}
	p, err := readPark(c.Waiting, input.Until, input.WaitingOn, input.Evidence, now)
	if err != nil {
		return Parked{}, false
	}
	if p.Evidence.Kind == "external_event" && parkReviewURL(p.Evidence.URL) {
		return Parked{}, false // the separately owned external-PR observer is not available here
	}
	return p, true
}

func externalCloseout(waiting string) bool {
	text := strings.ToLower(waiting)
	// i18n-ignore: these are literal Agent closeout inputs, not user-facing strings.
	if containsAny(text, []string{"approval", "permission", "reply", "review", "owner", "批准", "拍板", "回复", "复审", "返事", "承認"}) {
		return false
	}
	// i18n-ignore: literal Agent closeout inputs.
	return containsAny(text, []string{"deploy", "release", "event", "check run", "部署", "发布", "事件", "デプロイ", "リリース"})
}

func closeoutBinds(c proto.Closeout, artifacts []proto.WorkArtifact, e *ParkEvidence) bool {
	source := e.URL
	if e.Contact != nil && source == "" {
		source = e.Contact.URL
	}
	bound := source != "" && (strings.Contains(c.Waiting, source) || slices.ContainsFunc(artifacts, func(a proto.WorkArtifact) bool {
		return a.Ref == source
	}))
	if !bound {
		return false
	}
	text := strings.ToLower(c.Waiting)
	if strings.Contains(text, strings.ToLower(e.Target)) {
		return true
	}
	switch e.Kind {
	case "external_event":
		return externalCloseout(c.Waiting)
	case "merge_review":
		// i18n-ignore: literal Agent closeout inputs; the exact source already binds the recorded reviewer.
		return containsAny(text, []string{"review", "merge", "复审", "合并", "レビュー", "マージ"})
	case "person_reply":
		// i18n-ignore: literal Agent closeout inputs, paired with the exact sent-message source.
		return containsAny(text, []string{"reply", "respond", "回复", "返事"})
	}
	return false
}

func automaticDependency(p Parked) string {
	e := p.Evidence
	parts := []string{e.Kind, e.URL}
	if e.Contact != nil {
		parts = append(parts, strings.ToLower(e.Target), e.Contact.URL)
	}
	if e.Kind == "date" {
		parts = append(parts, p.Until.Format(time.RFC3339))
	}
	b, _ := json.Marshal(parts)
	return string(b)
}

func lastAutomatic(v *Supervision) *automaticPark {
	if v == nil || len(v.Reviews) == 0 {
		return nil
	}
	return v.Reviews[len(v.Reviews)-1].Automatic
}

// A third unchanged attempt goes to the supervisor. Replaying one stopped turn never spends another attempt.
func (s *Service) parkCloseout(ctx context.Context, t Todo, c proto.Session, now time.Time) bool {
	if s.agentSupervised(t) || c.UpdatedAt.After(now) {
		return false
	}
	_, reply := s.triageRead(ctx, c.MachineID, c)
	if reply == "" {
		reply = c.LastText
	}
	var previous *TaskReview
	if n := len(t.Supervision.Reviews); n > 0 {
		previous = &t.Supervision.Reviews[n-1]
	}
	p, ok := closeoutPark(reply, previous, now, s.location(ctx))
	if !ok {
		return false
	}
	dependency := automaticDependency(p)
	if p.Evidence.Kind == "merge_review" {
		fact := prFacts(t, p.Evidence.URL)
		if fact == nil || fact.State != "open" || fact.Draft || fact.Conflicts || fact.Review == "changes_requested" ||
			fact.Checks == "failing" || fact.CheckedAt.After(now) || now.Sub(fact.CheckedAt) > triagePRStatusFreshFor {
			return false
		}
		dependency += "|" + fact.HeadOID + "|" + fact.Review + "|" + fact.Checks
	}
	turn := c.MachineID + "/" + c.SessionID + "@" + c.UpdatedAt.UTC().Format(time.RFC3339Nano)
	latest, present := s.session(ctx, c.MachineID, c.SessionID)
	if !present || !sameTurn(latest, c.UpdatedAt) || latest.Status != proto.StatusDone ||
		latest.State == proto.StateWorking || latest.State == proto.StateNeedsYou ||
		latest.PendingInteractions > 0 || len(latest.Queued) != 0 || latest.TurnError != "" {
		return false
	}
	parked := false
	_, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		cur, exists := s.todoNow(t.ID)
		if !exists || cur.DoneAt != nil || v.state(cur) != "" && v.state(cur) != reviewRework || v.Waits != nil ||
			!v.UpdatedAt.Equal(t.Supervision.UpdatedAt) ||
			cur.PRURL != t.PRURL || !slices.Equal(cur.PRs, t.PRs) ||
			!slices.EqualFunc(cur.Agents, t.Agents, func(a, b TodoAgent) bool {
				return a.MachineID == b.MachineID && a.SessionID == b.SessionID &&
					(a.ReleasedAt == nil) == (b.ReleasedAt == nil) && (a.DismissedAt == nil) == (b.DismissedAt == nil)
			}) {
			return false
		}
		if p.Evidence.Kind == "merge_review" {
			fact := prFacts(cur, p.Evidence.URL)
			if fact == nil || fact.State != "open" || fact.Draft || fact.Conflicts ||
				dependency != automaticDependency(p)+"|"+fact.HeadOID+"|"+fact.Review+"|"+fact.Checks {
				return false
			}
		}
		a := automaticPark{Dependency: dependency, Turn: turn, Count: 1, FirstAt: now.UTC()}
		if prev := lastAutomatic(v); prev != nil && prev.Dependency == dependency {
			a = *prev
			if prev.Turn != turn {
				a.Count = min(maxAutomaticParks, a.Count+1)
			}
			a.Turn = turn
			if a.Count >= maxAutomaticParks || prev.Turn == turn {
				changed := prev.Turn != turn
				*v.Reviews[len(v.Reviews)-1].Automatic = a
				return changed
			}
		}
		v.State, v.Waits = reviewParked, nil
		v.Reviews = append(v.Reviews, TaskReview{At: now.UTC(), Verdict: reviewParked, By: "closeout",
			Reason: p.Reason, Until: p.Until, WaitingOn: p.WaitingOn, Parks: a.Count, Evidence: p.Evidence, Automatic: &a})
		if len(v.Reviews) > 20 {
			v.Reviews = v.Reviews[len(v.Reviews)-20:]
		}
		parked = true
		return true
	})
	if err != nil {
		s.logf("task %s: closeout parking failed: %v", t.ID, err)
		return false
	}
	return parked
}

// One morning batch, starting the day after admission; never one immediate model wake per new park.
func (s *Service) automaticParkSummary(ctx context.Context, t Todo, now time.Time) (superviseSignal, bool) {
	r := t.Supervision.parking()
	if t.DoneAt != nil || r == nil || r.Automatic == nil || !now.Before(r.Until) {
		return superviseSignal{}, false
	}
	a := r.Automatic
	day := now.Format(time.DateOnly)
	to := s.digestTo(s.settings(ctx), superviseSignal{Todo: t.ID})
	if now.Hour() < 9 || !a.FirstAt.In(now.Location()).Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())) ||
		to == "" || a.SummaryDay == day && a.SummaryTo == to {
		return superviseSignal{}, false
	}
	return superviseSignal{Todo: t.ID, Kind: sigParkedSummary, Key: day + "|" + a.Dependency,
		To: to, At: now}, true
}

func (s *Service) automaticParksTold(ctx context.Context, list []superviseSignal) {
	for _, g := range list {
		if g.Kind != sigParkedSummary {
			continue
		}
		_, _, err := s.updateSupervision(ctx, g.Todo, func(v *Supervision) bool {
			r := v.parking()
			if r == nil || r.Automatic == nil || g.Key != g.At.Format(time.DateOnly)+"|"+r.Automatic.Dependency {
				return false
			}
			r.Automatic.SummaryDay, r.Automatic.SummaryTo = g.At.Format(time.DateOnly), g.To
			return true
		})
		if err != nil {
			s.logf("task %s: automatic parking summary receipt: %v", g.Todo, err)
		}
	}
}
