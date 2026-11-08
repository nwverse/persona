package assistant

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector/github"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

var ciPRURL = regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/([1-9][0-9]*)\b`)
var ciPRNumber = regexp.MustCompile(`(?i)\b(?:pr|pull request)\s*#([1-9][0-9]*)\b`)
var ciArtifactNumber = regexp.MustCompile(`^#?[1-9][0-9]*$`)
var ciWaitSignal = regexp.MustCompile(`(?i)\b(ci|checks?|pipeline|workflows?|builds?|tests?)\b|持续集成|构建|测试|检查|ビルド|テスト|チェック`) // i18n-ignore: match model output
var ciOtherWait = regexp.MustCompile(`(?i)\b(review|reviews|approval|deployment|deploy)\b|审核|审查|评审|部署|上线|レビュー|承認|デプロイ`)     // i18n-ignore: match model output

// startCIWatch records only an unambiguous PR. A number without a repository
// can be checked only against the session directory's current branch.
func startCIWatch(c proto.Session, e *db.SessionWork, now time.Time, ask, reply string) {
	w := &e.Work
	if e.WakeAt == nil || w.On != proto.WaitExternal || w.ResetsAt != nil || c.Cwd == "" {
		return
	}
	wait := w.Wait + " " + w.Line
	if ciOtherWait.MatchString(wait) || !ciWaitSignal.MatchString(wait+" "+c.LastText+" "+reply) {
		return
	}
	texts := []string{w.Wait, w.Line, c.LastText, ask, reply}
	for _, a := range w.Artifacts {
		if a.Kind == "pr" || a.Kind == "url" {
			ref := strings.TrimSpace(a.Ref)
			if a.Kind == "pr" && ciArtifactNumber.MatchString(ref) {
				ref = "PR #" + strings.TrimPrefix(ref, "#")
			}
			texts = append(texts, ref)
		}
	}
	var repo string
	var number int
	for _, text := range texts {
		for _, match := range ciPRURL.FindAllStringSubmatch(text, -1) {
			n, _ := strconv.Atoi(match[2])
			if n == 0 || number != 0 && number != n || repo != "" && !strings.EqualFold(repo, match[1]) {
				return
			}
			repo, number = match[1], n
		}
		for _, match := range ciPRNumber.FindAllStringSubmatch(text, -1) {
			n, _ := strconv.Atoi(match[1])
			if n == 0 || number != 0 && number != n {
				return
			}
			number = n
		}
	}
	if number == 0 {
		return
	}
	w.CI = &proto.WorkCI{Repo: repo, Number: number, FallbackAt: *e.WakeAt}
	at := now.Add(time.Minute).UTC()
	if at.After(*e.WakeAt) {
		at = *e.WakeAt
	}
	e.WakeAt, w.WakeAt = &at, &at
}

// startCIPoll keeps slow providers from blocking unrelated work, while limiting
// concurrent requests and preventing overlapping polls for one session.
func (s *Service) startCIPoll(ctx context.Context, key [2]string, now time.Time) {
	s.work.mu.Lock()
	s.work.init()
	if s.work.ciPolling == nil {
		s.work.ciPolling = map[[2]string]bool{}
	}
	if s.work.ciPolling[key] || len(s.work.ciPolling) >= 3 {
		s.work.mu.Unlock()
		return
	}
	s.work.ciPolling[key] = true
	s.work.mu.Unlock()
	go func() {
		defer func() {
			s.work.mu.Lock()
			delete(s.work.ciPolling, key)
			s.work.mu.Unlock()
		}()
		s.workWake(ctx, s.newWorkView(ctx), key, now)
	}()
}

type ciResult struct {
	pending  bool
	failures []string
	count    int
}

// ciChecks first asks the active, authorized connector. The current-branch
// node RPC is safe for a number only after it returns the requested PR; a full
// page of 100 checks is refused because the RPC cannot prove it was complete.
func (s *Service) ciChecks(ctx context.Context, e db.SessionWork, c proto.Session) (ciResult, time.Time, bool) {
	ci := e.Work.CI
	// Connector access follows the same trust boundary as session tools. A
	// guest must not acquire the owner's credentials through a background watch.
	_, guest, err := s.guestOf(ctx, e.MachineID, e.SessionID)
	if err != nil || guest || s.isWorkMachine(e.MachineID) {
		return ciResult{}, time.Time{}, false
	}
	checks, hasChecks := s.ext.(interface {
		PRChecks(context.Context, string, int) (github.PRCheckSnapshot, time.Time, bool, error)
	})
	fromConnector := func(repo string) (ciResult, time.Time, bool) {
		if !hasChecks || repo == "" {
			return ciResult{}, time.Time{}, false
		}
		result, reset, available, err := checks.PRChecks(ctx, repo, ci.Number)
		if available && !reset.IsZero() {
			return ciResult{}, reset, true
		}
		if !available || err != nil {
			return ciResult{}, time.Time{}, false
		}
		return ciResult{result.Pending, result.Failures, result.Count}, time.Time{}, true
	}
	if result, reset, ok := fromConnector(ci.Repo); ok {
		return result, reset, true
	}
	var result struct {
		PR *struct {
			Number         int                            `json:"number"`
			URL            string                         `json:"url"`
			ChecksComplete bool                           `json:"checks_complete"`
			Checks         []struct{ Name, State string } `json:"checks"`
		} `json:"pr"`
	}
	if err := s.call(ctx, e.MachineID, "git.pr", map[string]any{"cwd": c.Cwd}, &result); err != nil {
		if text := strings.ToLower(err.Error()); strings.Contains(text, "rate limit") || strings.Contains(text, "429") {
			return ciResult{}, time.Now().Add(time.Hour), true
		}
		return ciResult{}, time.Time{}, false
	}
	if result.PR == nil || result.PR.Number != ci.Number {
		return ciResult{}, time.Time{}, false
	}
	match := ciPRURL.FindStringSubmatch(result.PR.URL)
	if len(match) != 3 || ci.Repo != "" && !strings.EqualFold(ci.Repo, match[1]) {
		return ciResult{}, time.Time{}, false
	}
	if ci.Repo == "" {
		if result, reset, ok := fromConnector(match[1]); ok {
			return result, reset, true
		}
	}
	if !result.PR.ChecksComplete || len(result.PR.Checks) >= 100 {
		return ciResult{}, time.Time{}, false
	}
	out := ciResult{count: len(result.PR.Checks)}
	for _, check := range result.PR.Checks {
		if check.Name == "" {
			out.pending = true
			continue
		}
		switch check.State {
		case "pass", "skipped":
		case "fail":
			out.failures = append(out.failures, check.Name)
		default:
			out.pending = true
		}
	}
	sort.Strings(out.failures)
	return out, time.Time{}, true
}

// pollCI returns a localized result only when every check completed. handled
// means the next action was persisted (or the watch was cancelled).
func (s *Service) pollCI(ctx context.Context, e db.SessionWork, c proto.Session, now time.Time) (string, bool) {
	if e.Work.CI == nil {
		return "", false
	}
	ci := *e.Work.CI
	pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	result, reset, available := s.ciChecks(pollCtx, e, c)
	cancel()
	key := [2]string{e.MachineID, e.SessionID}
	current, ok := s.workEntry(key)
	if !ok || current.WakeAt == nil || current.Work.CI == nil || !current.TurnAt.Equal(e.TurnAt) || *current.Work.CI != ci {
		return "", true
	}
	if _, why := s.workGuard(s.newWorkView(ctx), current, false); why != "" {
		if why == "offline" {
			at := now.Add(time.Minute).UTC()
			current.WakeAt, current.Work.WakeAt = &at, &at
		} else {
			current.WakeAt, current.Work.WakeAt, current.Work.CI = nil, nil, nil
		}
		s.replaceCI(e, current)
		return "", true
	}
	if !s.triageSwitches(ctx).Wake {
		current.WakeAt, current.Work.WakeAt, current.Work.CI = nil, nil, nil
		s.replaceCI(e, current)
		return "", true
	}
	if !available {
		current.Work.CI = nil
		at := ci.FallbackAt
		if at.Before(now) {
			at = now
		}
		current.WakeAt, current.Work.WakeAt = &at, &at
		s.replaceCI(e, current)
		return "", true
	}
	// An empty result may precede check creation, but a PR without checks must
	// still get the ordinary timed wake instead of waiting a full day.
	if reset.IsZero() && result.count == 0 && !now.Before(ci.FallbackAt) {
		current.Work.CI = nil
		at := now.UTC()
		current.WakeAt, current.Work.WakeAt = &at, &at
		s.replaceCI(e, current)
		return "", true
	}
	if !reset.IsZero() || result.pending || result.count == 0 {
		ci.Polls++
		current.Work.CI = &ci
		if ci.Polls >= 240 || now.After(current.TurnAt.Add(24*time.Hour)) {
			current.Work.CI = nil
			at := now.UTC()
			if reset.After(at) {
				at = reset.Add(time.Minute).UTC()
			}
			current.WakeAt, current.Work.WakeAt = &at, &at
		} else {
			delay := time.Minute * time.Duration(1<<min(ci.Polls/5, 3))
			at := now.Add(delay).UTC()
			if reset.After(at) {
				at = reset.Add(time.Minute).UTC()
			}
			current.WakeAt, current.Work.WakeAt = &at, &at
		}
		s.replaceCI(e, current)
		return "", true
	}
	if len(result.failures) == 0 {
		return workT(s.lang(ctx), "ci_passed"), false
	}
	return workT(s.lang(ctx), "ci_failed", i18n.Args{"checks": ciCheckNames(result.failures)}), false
}

// ciCheckNames are untrusted external data. Quote and escape each bounded name
// so it cannot add lines or close a markup delimiter in the wake message.
func ciCheckNames(names []string) string {
	out := make([]string, 0, min(len(names), 5))
	for _, name := range names {
		if len(out) == 5 {
			break
		}
		out = append(out, strconv.Quote(html.EscapeString(clip(oneLine(name), 40))))
	}
	if len(names) > len(out) {
		out = append(out, "…")
	}
	return strings.Join(out, ", ")
}

// replaceCI applies a poll only while its original watch is still current.
// A user action or a new turn must win even if it arrived during the network call.
func (s *Service) replaceCI(before, after db.SessionWork) {
	key := [2]string{before.MachineID, before.SessionID}
	s.work.mu.Lock()
	current := s.work.entries[key]
	if current == nil || current.WakeAt == nil || before.WakeAt == nil || !current.WakeAt.Equal(*before.WakeAt) ||
		!current.TurnAt.Equal(before.TurnAt) || current.Work.CI == nil || before.Work.CI == nil || *current.Work.CI != *before.Work.CI {
		s.work.mu.Unlock()
		return
	}
	after.UpdatedAt = time.Now().UTC()
	*current = after
	s.work.mu.Unlock()
	s.saveWork(after)
	s.publishSoon()
}

// prepareCI persists the exact wake before any send. A restart can retry it
// with the same client message id through the hub’s once-only guard.
func (s *Service) prepareCI(ctx context.Context, e db.SessionWork, text string) bool {
	key := [2]string{e.MachineID, e.SessionID}
	unlock := s.lockWorkWrite(key)
	defer unlock()
	s.work.mu.Lock()
	current := s.work.entries[key]
	if current == nil || current.WakeAt == nil || current.Work.CI == nil || !current.TurnAt.Equal(e.TurnAt) || current.Work.CI.Message != "" || e.Work.CI == nil || *current.Work.CI != *e.Work.CI {
		s.work.mu.Unlock()
		return false
	}
	saved := *current
	ci := *current.Work.CI
	ci.Message = text
	saved.Work.CI = &ci
	saved.Wakes = e.Wakes
	saved.UpdatedAt = time.Now().UTC()
	s.work.mu.Unlock()
	if st := s.workDB(); st != nil {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := st.PutSessionWork(writeCtx, saved)
		cancel()
		if err != nil {
			s.logf("work: save CI wake: %v", err)
			return false
		}
	}
	s.work.mu.Lock()
	current = s.work.entries[key]
	if current == nil || current.WakeAt == nil || current.Work.CI == nil || !current.TurnAt.Equal(saved.TurnAt) || current.Work.CI.Message != "" || e.Work.CI == nil || *current.Work.CI != *e.Work.CI {
		s.work.mu.Unlock()
		return false
	}
	*current = saved
	s.work.mu.Unlock()
	s.publishSoon()
	return true
}

// deliverCI uses the persisted outbox and the hub’s durable message-id guard.
// An uncertain delivery is never repeated by the hub.
func (s *Service) deliverCI(ctx context.Context, e db.SessionWork, now time.Time) {
	if e.Work.CI == nil || e.Work.CI.Message == "" {
		return
	}
	if _, why := s.workGuard(s.newWorkView(ctx), e, false); why != "" || !s.triageSwitches(ctx).Wake {
		return
	}
	sum := sha256.Sum256([]byte(e.MachineID + "\x00" + e.SessionID + "\x00" + e.TurnAt.UTC().Format(time.RFC3339Nano)))
	id := fmt.Sprintf("ci-%x", sum)
	key := [2]string{e.MachineID, e.SessionID}
	s.work.mu.Lock()
	current := s.work.entries[key]
	if current == nil || current.WakeAt == nil || current.Work.CI == nil || !current.TurnAt.Equal(e.TurnAt) || current.Work.CI.Message != e.Work.CI.Message {
		s.work.mu.Unlock()
		return
	}
	s.work.sent[key] = time.Now()
	s.work.mu.Unlock()
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := s.call(sendCtx, e.MachineID, DeviceSessionSendOnce, map[string]any{"session_id": e.SessionID, "text": e.Work.CI.Message, "client_msg_id": id}, nil)
	cancel()
	s.work.mu.Lock()
	current = s.work.entries[key]
	if current == nil || current.WakeAt == nil || current.Work.CI == nil || !current.TurnAt.Equal(e.TurnAt) || current.Work.CI.Message != e.Work.CI.Message {
		s.work.mu.Unlock()
		return
	}
	if err != nil {
		at := now.Add(2 * time.Minute).UTC()
		current.WakeAt, current.Work.WakeAt = &at, &at
	} else {
		current.WakeAt, current.Work.WakeAt, current.Work.CI = nil, nil, nil
	}
	saved := *current
	s.work.mu.Unlock()
	s.saveWork(saved)
	s.publishSoon()
	if err != nil {
		s.logf("work %s/%s: CI wake: %v", e.MachineID, short(e.SessionID), err)
		return
	}
	s.workEvent(e.MachineID, e.SessionID, "woke", map[string]any{"attempt": e.Wakes, "after": now.Sub(e.TurnAt).Seconds()})
}
