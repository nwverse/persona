package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
)

// The hand-off to one Agent run (docs/extensions/secret-providers.md "The hand-off to one Agent run", #3962): after
// the user approved the card, the manager that connects this computer stages the values here over its own connection
// (secret.stage) and gets a one-time ticket. The Agent's start carries only the ticket (session.start's
// secret_ticket); its start command runs `personad launch --ticket <ticket> …`, which fetches them from node.sock
// with this machine's launch key and execs the Agent with them in its environment. They are never on a command line,
// typed into a pane, or written to disk by Persona.
//
// A ticket works once and for at most proto.SecretStageKeep: an engine's resume after a restart, a later turn or
// another Agent starts without them. Until the node exits it keeps every value it was handed (at most
// secretScrubMost) to hide them from everything it sends: a best-effort guard, since the Agent may still write a
// value into its own files on this computer.

// secretScrubMost is how many handed-over values the node keeps hiding; the oldest go first.
const secretScrubMost = 64

type stagedSecrets struct {
	env     []proto.SecretEnvVar
	expires time.Time
}

// secretStage is the node's staged hand-offs and the values it hides.
type secretStage struct {
	mu     sync.Mutex
	staged map[string]stagedSecrets // by ticket
	scrub  *secretScrubber
}

// handoffs is the node's staged hand-offs, made at first use (a test's Node{} has none yet).
func (n *Node) handoffs() *secretStage {
	n.secretsOnce.Do(func() {
		n.secrets = &secretStage{staged: map[string]stagedSecrets{}, scrub: &secretScrubber{}}
	})
	return n.secrets
}

// stage answers secret.stage: the values checked, kept until the ticket it answers is used or expires.
func (n *Node) secretStageCall(params json.RawMessage) (any, error) {
	var p proto.SecretStageParams
	err := json.Unmarshal(params, &p)
	clear(params) // the values' base64; p.Env holds them decoded from here
	if err != nil {
		return nil, err
	}
	if p.Check {
		return proto.SecretStageResult{}, nil
	}
	if err := checkSecretEnv(p.Env); err != nil {
		clearEnv(p.Env)
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		clearEnv(p.Env)
		return nil, err
	}
	ticket := hex.EncodeToString(b)
	expires := time.Now().Add(proto.SecretStageKeep)
	s := n.handoffs()
	s.mu.Lock()
	s.dropExpired(time.Now())
	s.staged[ticket] = stagedSecrets{env: p.Env, expires: expires}
	s.mu.Unlock()
	for _, v := range p.Env {
		s.scrub.add(v.Value) // hidden from now on, the start's own output included
	}
	time.AfterFunc(proto.SecretStageKeep, func() { s.take(ticket, false) }) // not used: gone
	return proto.SecretStageResult{Ticket: ticket, ExpiresAt: expires.UTC()}, nil
}

func checkSecretEnv(env []proto.SecretEnvVar) error {
	if len(env) == 0 || len(env) > proto.SecretEnvMost {
		return errors.New("secret.stage: 1 to 8 variables")
	}
	seen := map[string]bool{}
	for _, v := range env {
		if why := compat.SecretEnvNameProblem(v.Name); why != "" {
			return errors.New("secret.stage: " + v.Name + ": " + why)
		}
		if seen[v.Name] {
			return errors.New("secret.stage: " + v.Name + " twice")
		}
		seen[v.Name] = true
		if len(v.Value) < proto.SecretEnvValueLeast || len(v.Value) > proto.SecretEnvValueMost || bytes.IndexByte(v.Value, 0) >= 0 {
			return errors.New("secret.stage: " + v.Name + ": a value of 8 bytes to 16 KB without NUL")
		}
	}
	return nil
}

func clearEnv(env []proto.SecretEnvVar) {
	for _, v := range env {
		clear(v.Value)
	}
}

func (s *secretStage) dropExpired(now time.Time) {
	for t, st := range s.staged {
		if now.After(st.expires) {
			clearEnv(st.env)
			delete(s.staged, t)
		}
	}
}

// take removes ticket's values and returns them (use: the caller zeroes them after use); nil when it is unknown,
// used or expired. Without use they are zeroed here.
func (s *secretStage) take(ticket string, use bool) []proto.SecretEnvVar {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	delete(s.staged, ticket)
	if !ok {
		return nil
	}
	if !use || time.Now().After(st.expires) {
		clearEnv(st.env)
		return nil
	}
	return st.env
}

// has: ticket is staged and good now (a start checks before it opens a tab; the wrapper takes it).
func (s *secretStage) has(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	return ok && time.Now().Before(st.expires)
}

// errSecretTicket is a start's ticket that is not staged here (unknown, used or expired).
var errSecretTicket = errors.New("the values approved for this start are no longer here (the card was answered more than two minutes ago, or they were used): approve it again")

// secretEnvFor answers node.sock's secret.env: ticket's variables for a request that carries the machine's launch
// key, the ticket used up either way. Any other request gets the same error whatever it names.
func (n *Node) secretEnvFor(req sockRequest) map[string]any {
	key, err := ensureLaunchKey(nodeclient.LaunchKeyPath())
	if err != nil {
		return map[string]any{"error": "the node cannot read its launch key: " + err.Error()}
	}
	if subtle.ConstantTimeCompare([]byte(req.LaunchKey), []byte(key)) != 1 {
		n.logf("secret.env refused: the request did not carry this machine's launch key")
		return map[string]any{"error": "refused: the values go only to a personad launch that can read this machine's launch key"}
	}
	env := n.handoffs().take(req.Ticket, true)
	if env == nil {
		return map[string]any{"error": errSecretTicket.Error()}
	}
	out := make([]string, 0, len(env))
	for _, v := range env {
		out = append(out, v.Name+"="+string(v.Value))
	}
	clearEnv(env)
	n.logf("secret.env: %d variable(s) handed to a start", len(out))
	return map[string]any{"env": out}
}

// secretEnvRequest is what `personad launch --ticket` asks the node for.
func secretEnvRequest(ticket string) sockRequest {
	key, _ := readLaunchKey(nodeclient.LaunchKeyPath())
	return sockRequest{Kind: "secret.env", Ticket: ticket, LaunchKey: key}
}

// fetchSecretEnv is ticket's variables (NAME=value) from the local node, for the process about to exec the Agent.
func fetchSecretEnv(ctx context.Context, ticket string) ([]string, error) {
	line, err := nodeclient.Dial(ctx, secretEnvRequest(ticket))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Env   []string `json:"env"`
		Error string   `json:"error"`
	}
	err = json.Unmarshal(line, &resp)
	clear(line)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Env, nil
}

// ---- hiding them ----

// secretScrubber hides handed-over values in what the node sends: each one as it is, as JSON writes it inside a
// string, and in base64.
type secretScrubber struct {
	mu   sync.RWMutex
	find [][]byte // longest first
	any  bool
}

func (s *secretScrubber) add(value []byte) {
	forms := [][]byte{bytes.Clone(value)}
	for _, html := range []bool{false, true} { // the node's own lines leave < > & as they are; other encoders escape them
		var j bytes.Buffer
		enc := json.NewEncoder(&j)
		enc.SetEscapeHTML(html)
		if enc.Encode(string(value)) == nil {
			quoted := bytes.TrimSuffix(j.Bytes(), []byte("\n")) // "…", the encoder's newline off
			if inner := quoted[1 : len(quoted)-1]; !bytes.Equal(inner, value) {
				forms = append(forms, bytes.Clone(inner))
			}
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		forms = append(forms, []byte(enc.EncodeToString(value)))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range forms {
		if !slices.ContainsFunc(s.find, func(x []byte) bool { return bytes.Equal(x, f) }) {
			s.find = append(s.find, f)
		}
	}
	for len(s.find) > secretScrubMost*6 {
		s.find = s.find[1:]
	}
	slices.SortStableFunc(s.find, func(a, b []byte) int { return len(b) - len(a) })
	s.any = true
}

// active: there is something to hide.
func (s *secretScrubber) active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.any
}

// bytes is b with every value replaced by proto.Redacted (b itself when there is none in it).
func (s *secretScrubber) bytes(b []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.find {
		if bytes.Contains(b, f) {
			b = bytes.ReplaceAll(b, f, []byte(proto.Redacted))
		}
	}
	return b
}

// line is one JSON line with every value inside its strings replaced by proto.Redacted, and only there: a match that
// starts inside an escape sequence or crosses a string's end is left, so the line stays valid JSON.
func (s *secretScrubber) line(b []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.find) == 0 {
		return b
	}
	hit := false
	for _, f := range s.find {
		if bytes.Contains(b, f) {
			hit = true
			break
		}
	}
	if !hit {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b))
	i := 0
	for i < len(b) {
		if b[i] != '"' {
			out.WriteByte(b[i])
			i++
			continue
		}
		// A string: find its end, then replace inside [i+1, end).
		end := i + 1
		for end < len(b) && b[end] != '"' {
			if b[end] == '\\' {
				end++
			}
			end++
		}
		if end > len(b) {
			end = len(b)
		}
		out.WriteByte('"')
		out.Write(s.inString(b[i+1 : end]))
		if end < len(b) {
			out.WriteByte('"')
		}
		i = end + 1
	}
	return out.Bytes()
}

// inString replaces the values in str, the inside of a JSON string, at the start of a character or escape only.
func (s *secretScrubber) inString(str []byte) []byte {
	var out []byte
	i, last := 0, 0
	for i < len(str) {
		matched := 0
		for _, f := range s.find {
			if bytes.HasPrefix(str[i:], f) {
				matched = len(f)
				break
			}
		}
		if matched > 0 {
			out = append(out, str[last:i]...)
			out = append(out, proto.Redacted...)
			i += matched
			last = i
			continue
		}
		if str[i] == '\\' { // an escape is one unit
			i += 2
			continue
		}
		i++
	}
	if out == nil {
		return str
	}
	return append(out, str[last:]...)
}

// scrubWriter is the node's connection with the values hidden in every line it writes (Serve). rpc.Peer writes
// one whole line a call.
type scrubWriter struct {
	w io.Writer
	s *secretScrubber
}

func (w scrubWriter) Write(p []byte) (int, error) {
	if !w.s.active() {
		return w.w.Write(p)
	}
	if _, err := w.w.Write(w.s.line(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// scrubBytes is b with the handed-over values hidden, for bytes the node sends as base64 (a terminal's frames), which
// the connection's hiding cannot see into. A value split between two frames is not found.
func (n *Node) scrubBytes(b []byte) []byte {
	s := n.handoffs().scrub
	if !s.active() {
		return b
	}
	return s.bytes(bytes.Clone(b))
}

// ---- the start ----

const (
	secretEnvAgentCode = "secret_env_agent"
	secretEnvGoneCode  = "secret_env_gone"
	secretEnvAgentText = "secrets can be handed only to a new Claude Code start in a terminal, with a provider added in Persona or plain claude"
)

// secretStartProblem is why a start cannot take ticket: only a new Claude Code start whose command personad can exec
// (a provider added in Persona, or plain claude; not a function from the shell's rc file), with the ticket staged.
func (n *Node) secretStartProblem(kind, launcher, resume, ticket string) error {
	if kind != "claude" || resume != "" {
		return refusal(secretEnvAgentCode, secretEnvAgentText, nil)
	}
	if _, managed := n.managedLauncher(launcher); !managed && launcher != "claude" {
		return refusal(secretEnvAgentCode, secretEnvAgentText, nil)
	}
	if !n.handoffs().has(ticket) {
		return refusal(secretEnvGoneCode, errSecretTicket.Error(), nil)
	}
	return nil
}

// withSecretTicket is line, a Claude command line from claudeCommandLine, started through `personad launch --ticket`:
// the launcher's own word replaced, the rest as it was.
func (n *Node) withSecretTicket(line, launcher, ticket string, q func(string) string) string {
	rest := strings.TrimPrefix(line, n.launcherCommandQ(launcher, q))
	wrap := q(n.bin) + " launch --ticket " + q(ticket) + " "
	if _, managed := n.managedLauncher(launcher); managed {
		return wrap + launcher + rest
	}
	word := "claude" // plain claude, with the start directory it may carry in front (launcherCommandQ)
	if d := n.claudeStartDir.Load(); d != nil && *d != "" {
		return "CLAUDE_CONFIG_DIR=" + shellDirQ(*d, q) + " " + wrap + "-- " + word + rest
	}
	return wrap + "-- " + word + rest
}
