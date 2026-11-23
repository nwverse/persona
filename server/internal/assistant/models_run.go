package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// An API purpose may pick a launcher without a gateway the manager can call (plain claude, codex, agy, or a computer's
// own launcher): each request then runs that launcher's CLI headless once on the main chat's computer, which answers
// with the final text (docs/assistant/model-config.md "Running API purposes on a launcher", #5823, #12027). The manager
// still resolves the purpose and reads the answer; the node runs the CLI with no tools and leaves no session (model.run).

// modelRunTimeout bounds a run whose caller set no deadline.
const modelRunTimeout = 2 * time.Minute

// The launcher's CLI starts cold for every request: agy's isolated runs took 12–48 s on a Linux hub machine where
// claude's and codex's take seconds (#13443). runFloorBook keeps, for each launcher on each computer, how fast a run
// there has been seen to be, so a request whose caller cannot wait that long (a new Agent's tab name waits 8 s on the
// start's path) fails at once instead of starting a run that is cancelled unanswered while its caller waits.

// runFloorTTL is how long what was seen counts: after it the next request runs, whatever it showed. runFloorSlack is
// how much longer than the floor a request must be able to wait to be sent (the hop to the computer, the CLI's exit).
var runFloorTTL, runFloorSlack = time.Hour, time.Second

// runFloorMisses: waits that ended unanswered, in a row, before they hold requests back: one slow run (a computer
// waking up) leaves the next request alone. An answer counts at once.
const runFloorMisses = 2

type runFloorBook struct {
	mu sync.Mutex
	m  map[string]runFloor // runFloorKey → what was seen
}

type runFloor struct {
	fastest time.Duration // the shortest run that answered; 0: none
	waited  time.Duration // the shortest of the waits that ended unanswered since the last answer
	misses  int           // waits that ended unanswered since the last answer
	at      time.Time     // when it last changed
}

func runFloorKey(c ai.Config) string { return c.ViaMachine + "\x00" + c.RunLauncher }

// floor is how long a run of key has recently taken at least; false when nothing seen says so.
func (b *runFloorBook) floor(key string, now time.Time) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f, ok := b.m[key]
	switch {
	case !ok || now.Sub(f.at) > runFloorTTL:
		return 0, false
	case f.fastest > 0:
		return f.fastest, true
	case f.misses >= runFloorMisses:
		return f.waited, true
	}
	return 0, false
}

// note records a run of key that answered after took, or (answered false) a wait of took that ended without an answer.
func (b *runFloorBook) note(key string, took time.Duration, answered bool, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]runFloor{}
	}
	f := b.m[key]
	if now.Sub(f.at) > runFloorTTL {
		f = runFloor{}
	}
	if answered {
		if f.fastest == 0 || took < f.fastest {
			f.fastest = took
		}
		f.waited, f.misses = 0, 0
	} else {
		if f.misses == 0 || took < f.waited {
			f.waited = took
		}
		f.misses++
	}
	f.at = now
	b.m[key] = f
}

// runTooSlowError is a request that was never sent: its caller could not wait as long as runs of its launcher there
// take (runFloorBook). It reads as a timeout to its caller and is not counted as a model call (countedClient).
type runTooSlowError struct{ msg string }

func (e *runTooSlowError) Error() string        { return e.msg }
func (e *runTooSlowError) Is(target error) bool { return target == context.DeadlineExceeded }

// runHeldBack: err is a request runFloorBook held back, which never reached the computer.
func runHeldBack(err error) bool {
	var skipped *runTooSlowError
	return errors.As(err, &skipped)
}

// purposeOnLauncher: purpose id's requests run on a launcher's CLI (model.run), which starts cold for each of them.
func (s *Service) purposeOnLauncher(ctx context.Context, id string) bool {
	b, err := s.resolveModel(ctx, id)
	if err != nil {
		return false
	}
	c, err := s.apiConfig(ctx, b)
	return err == nil && c.RunLauncher != ""
}

// runClient is the client of c, a launcher on c.ViaMachine (c.RunLauncher).
func (s *Service) runClient(c ai.Config) *ai.Client {
	return ai.NewRun(c, func(ctx context.Context, r ai.Request) (ai.Reply, error) { return s.modelRun(ctx, c, r) })
}

// modelRun answers r with one model.run on c's computer, unless its caller cannot wait as long as runs there take.
func (s *Service) modelRun(ctx context.Context, c ai.Config, r ai.Request) (ai.Reply, error) {
	if proto.IsExternalLauncher(c.RunLauncher) {
		return ai.Reply{}, i18n.Err("external-adapter.purpose_unsupported")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, modelRunTimeout)
		defer cancel()
	}
	key := runFloorKey(c)
	d, _ := ctx.Deadline()
	if floor, ok := s.runFloors.floor(key, time.Now()); ok && time.Until(d) < floor+runFloorSlack {
		return ai.Reply{}, s.runTooSlow(ctx, c, floor, time.Until(d))
	}
	p := proto.ModelRunParams{Launcher: c.RunLauncher, Model: c.Model, System: r.System, Prompt: runPrompt(r.Messages)}
	effortOK := slices.Contains(proto.EffortLevels, c.ReasoningEffort) ||
		(slices.Contains(proto.CodexEffortLevels, c.ReasoningEffort) && s.runLauncherAgent(ctx, c.ViaMachine, c.RunLauncher) == "codex")
	if effortOK { // "off" / "none" (and Codex-only levels on non-Codex launchers) defer to the launcher's own default
		p.Effort = c.ReasoningEffort
	}
	_, p.Configured = s.configuredLauncher(ctx, c.ViaMachine, c.RunLauncher)
	p.TimeoutMS = max(time.Until(d).Milliseconds(), 1)
	raw, err := json.Marshal(p)
	if err != nil {
		return ai.Reply{}, err
	}
	var out json.RawMessage
	sent := time.Now()
	if s.devs == nil {
		err = devices.ErrNoMachine
	} else {
		out, err = s.devs.CallNode(ctx, c.ViaMachine, proto.MethodModelRun, raw)
	}
	if err != nil {
		if runTimedOut(ctx, err) {
			s.runFloors.note(key, time.Since(sent), false, time.Now())
		}
		return ai.Reply{}, s.modelRunErr(ctx, c, err)
	}
	var a proto.ModelRunAnswer
	if err := json.Unmarshal(out, &a); err != nil {
		return ai.Reply{}, fmt.Errorf("model.run: bad answer: %w", err)
	}
	s.runFloors.note(key, time.Since(sent), true, time.Now())
	return ai.Reply{Text: a.Text, Model: a.Model, Usage: a.Usage}, nil
}

// runTimedOut: a model.run ended because its time was up (the node's own timer or this caller's deadline), not
// because its caller gave up or it failed.
func runTimedOut(ctx context.Context, err error) bool {
	var re *rpc.Error
	return (errors.As(err, &re) && re.Code == proto.CodeModelRunTimeout) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		errors.Is(err, context.DeadlineExceeded)
}

// runTooSlow says why a request was not sent to c's computer: runs there have taken floor at least, and its caller
// could wait only left.
func (s *Service) runTooSlow(ctx context.Context, c ai.Config, floor, left time.Duration) error {
	name := c.ViaMachine
	if d, ok := s.device(ctx, c.ViaMachine); ok && d.Name != "" {
		name = d.Name
	}
	return &runTooSlowError{msg: i18n.T(s.lang(ctx), "assistant-models.err_run_too_slow", i18n.Args{
		"name": name, "launcher": c.RunLauncher,
		"took": int(math.Ceil(floor.Seconds())), "wait": int(math.Round(max(left, 0).Seconds())),
	})}
}

// runLauncherAgent resolves the CLI agent behind launcher name on machine for model.run effort filtering.
func (s *Service) runLauncherAgent(ctx context.Context, machine, name string) string {
	if l, ok := s.configuredLauncher(ctx, machine, name); ok {
		if agent := cmp.Or(l.AgentType, l.Agent); agent != "" {
			return agent
		}
	}
	for _, l := range s.storedLaunchers(ctx) {
		if l.Name == name {
			return l.AgentType()
		}
	}
	if machine != "" {
		for _, m := range s.zshrcLaunchers(ctx, false) {
			if m.MachineID != machine {
				continue
			}
			for _, l := range m.Launchers {
				if l.Name == name {
					return viewAgent(LauncherView{Name: l.Name, Agent: l.Agent})
				}
			}
		}
	}
	for _, v := range s.launcherViews(ctx) {
		if v.Name == name {
			return viewAgent(v)
		}
	}
	return viewAgent(LauncherView{Name: name})
}

// runPrompt is the conversation as the CLI reads it: a single user turn as it is, several as one transcript.
func runPrompt(turns []ai.Message) string {
	if len(turns) == 1 && turns[0].Role == ai.RoleUser {
		return turns[0].Content
	}
	in := make([]prompts.LauncherRunTurn, len(turns))
	for i, t := range turns {
		in[i] = prompts.LauncherRunTurn{Assistant: t.Role == ai.RoleAssistant, Text: t.Content}
	}
	return prompts.RenderLauncherRunInput(in)
}

// modelRunErr says what went wrong in the user's language: the computer offline or gone, the CLI not logged in there,
// no answer in time. The node's other errors (an old node, what the CLI printed) go as they are.
func (s *Service) modelRunErr(ctx context.Context, c ai.Config, err error) error {
	if errors.Is(err, devices.ErrNoMachine) {
		return errViaMachine(c.ViaMachine)
	}
	name := c.ViaMachine
	if d, ok := s.device(ctx, c.ViaMachine); ok && d.Name != "" {
		name = d.Name
	}
	args := i18n.Args{"name": name, "launcher": c.RunLauncher}
	var re *rpc.Error
	switch {
	case errors.Is(err, apierr.ErrOffline), errors.Is(err, rpc.ErrClosed):
		return errors.New(i18n.T(s.lang(ctx), "assistant-models.err_run_offline", args))
	case errors.As(err, &re) && re.Code == proto.CodeModelRunLoggedOut:
		return errors.New(i18n.T(s.lang(ctx), "assistant-models.err_run_logged_out", args))
	case errors.As(err, &re) && re.Code == proto.CodeModelRunTimeout, errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-models.err_run_timeout", args), context.DeadlineExceeded)
	}
	return err
}
