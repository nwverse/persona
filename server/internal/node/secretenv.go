package node

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
)

// The hand-off to one Agent run (docs/extensions/secret-providers.md "The hand-off to one Agent run", #3962): after
// the user approved the card, the manager that connects this computer asks this node for a one-time key
// (secret.stage {check}), seals the values to it and stages them here (secret.stage {key_id, sealed}), for a one-time
// ticket. Whatever relays the manager's connection (the hub, for a Persona Link or a mesh computer) sees no value. The
// Agent's start carries only the ticket (session.start's secret_ticket); its start command runs `personad launch
// --ticket <ticket> …`, which fetches them from node.sock with this machine's launch key and execs the Agent with them
// in its environment. They are never on a command line, typed into a pane, or written to disk by Persona.
//
// A key and a ticket each work once and for at most proto.SecretStageKeep; the first start that names a ticket takes
// it, and an engine's resume after a restart, a later turn or another Agent starts without the values. The process
// (every connection's Node in it: attach makes one per manager connection) keeps the values it was handed, the last
// secretScrubMost, to hide them in everything it sends: a best-effort guard, since the Agent may still write a value
// into its own files on this computer.

// secretScrubMost is how many handed-over values the process keeps hiding; the oldest go first.
const secretScrubMost = 64

type stagedSecrets struct {
	env      []proto.SecretEnvVar
	expires  time.Time
	reserved bool // a start named it: no other start may (secretStage.reserve)
}

type stageKey struct {
	priv    *ecdh.PrivateKey
	expires time.Time
}

// secretStage is the process's stage keys, staged hand-offs and the values it hides.
type secretStage struct {
	mu     sync.Mutex
	keys   map[string]stageKey      // by key id
	staged map[string]stagedSecrets // by ticket
	scrub  *secretScrubber
}

func newSecretStage() *secretStage {
	return &secretStage{keys: map[string]stageKey{}, staged: map[string]stagedSecrets{}, scrub: &secretScrubber{}}
}

var (
	processStageOnce sync.Once
	processStage     *secretStage
)

// handoffs is the process's hand-offs, shared by every connection's Node: a value staged under one connection is
// still hidden, and its ticket still good, after the manager reconnects. A test may give its Node its own.
func (n *Node) handoffs() *secretStage {
	if n.secrets != nil {
		return n.secrets
	}
	processStageOnce.Do(func() { processStage = newSecretStage() })
	return processStage
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// secretStageCall answers secret.stage: to a check, a one-time key; to a hand-off sealed to one, a one-time ticket.
func (n *Node) secretStageCall(params json.RawMessage) (any, error) {
	var p proto.SecretStageParams
	err := json.Unmarshal(params, &p)
	clear(params)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		// personad launch stays alive as claude's parent there (osproc.Exec), holding the values for the run.
		return nil, errors.New("secret.stage: secrets are not handed to Agents on Windows yet")
	}
	s := n.handoffs()
	now := time.Now()
	if p.Check {
		priv, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		id, err := randomHex(16)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.dropExpired(now)
		s.keys[id] = stageKey{priv: priv, expires: now.Add(proto.SecretStageKeep)}
		s.mu.Unlock()
		return proto.SecretStageResult{Key: base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), KeyID: id}, nil
	}
	s.mu.Lock()
	k, ok := s.keys[p.KeyID]
	delete(s.keys, p.KeyID) // one use, opened or not
	s.mu.Unlock()
	if !ok || now.After(k.expires) {
		return nil, errors.New("secret.stage: the key is unknown, used or expired: ask for a new one")
	}
	env, err := proto.OpenSecretEnv(k.priv, p.KeyID, p.Sealed)
	clear(p.Sealed)
	if err != nil {
		return nil, errors.New("secret.stage: " + err.Error())
	}
	if err := checkSecretEnv(env); err != nil {
		clearEnv(env)
		return nil, err
	}
	ticket, err := randomHex(32)
	if err != nil {
		clearEnv(env)
		return nil, err
	}
	expires := now.Add(proto.SecretStageKeep)
	for _, v := range env {
		s.scrub.add(v.Value) // hidden from now on, the start's own output included
	}
	s.mu.Lock()
	s.dropExpired(now)
	s.staged[ticket] = stagedSecrets{env: env, expires: expires}
	s.mu.Unlock()
	time.AfterFunc(proto.SecretStageKeep, func() { s.take(ticket, false) }) // not used in time: gone
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
			return errors.New("secret.stage: " + v.Name + ": a value of 16 bytes to 16 KB without NUL")
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
	for id, k := range s.keys {
		if now.After(k.expires) {
			delete(s.keys, id)
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

// has: ticket is staged, good now and no start's yet (session.start checks before it starts; the start reserves it).
func (s *secretStage) has(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	return ok && !st.reserved && time.Now().Before(st.expires)
}

// reserve gives ticket to the start that names it first: false when it is unknown, expired or another start has it.
func (s *secretStage) reserve(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	if !ok || st.reserved || time.Now().After(st.expires) {
		return false
	}
	st.reserved = true
	s.staged[ticket] = st
	return true
}

// errSecretTicket is a start's ticket that is not staged here (unknown, used, expired, or another start's).
var errSecretTicket = errors.New("the values approved for this start are no longer here (the card was answered more than two minutes ago, or they were used): approve it again")

// secretEnvFor answers node.sock's secret.env: ticket's variables for a request that carries the machine's launch
// key, the ticket used up. A request without the key gets the same error whatever it names, and leaves the ticket.
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
// The names are checked again here: the process that execs is the last to see them.
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
	for _, kv := range resp.Env {
		name, _, _ := strings.Cut(kv, "=")
		if why := compat.SecretEnvNameProblem(name); why != "" {
			return nil, errors.New(name + ": " + why)
		}
	}
	return resp.Env, nil
}

// ---- hiding them ----

// secretScrubber hides handed-over values in what the node sends: inside JSON strings as JSON writes them, in raw
// bytes as they are, and their base64 anywhere.
type secretScrubber struct {
	mu     sync.RWMutex
	values [][]byte // oldest first, at most secretScrubMost
	json   [][]byte // each value as it reads inside a JSON string (both HTML escapings), longest first
	raw    [][]byte // each value as it is, longest first
	b64    [][]byte // each value's base64 (standard and URL, padded and not), longest first
}

func (s *secretScrubber) add(value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.values, func(v []byte) bool { return bytes.Equal(v, value) }) {
		return
	}
	s.values = append(s.values, bytes.Clone(value))
	if len(s.values) > secretScrubMost {
		clear(s.values[0])
		s.values = s.values[1:]
	}
	s.json, s.raw, s.b64 = nil, nil, nil
	for _, v := range s.values {
		s.raw = appendForm(s.raw, v)
		for _, html := range []bool{false, true} { // the node's own lines leave < > & as they are; other encoders escape them
			var j bytes.Buffer
			enc := json.NewEncoder(&j)
			enc.SetEscapeHTML(html)
			if enc.Encode(string(v)) == nil {
				quoted := bytes.TrimSuffix(j.Bytes(), []byte("\n"))
				s.json = appendForm(s.json, quoted[1:len(quoted)-1])
			}
		}
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			s.b64 = appendForm(s.b64, []byte(enc.EncodeToString(v)))
		}
	}
	for _, l := range [][][]byte{s.json, s.raw, s.b64} {
		slices.SortStableFunc(l, func(a, b []byte) int { return len(b) - len(a) })
	}
}

func appendForm(list [][]byte, f []byte) [][]byte {
	if slices.ContainsFunc(list, func(x []byte) bool { return bytes.Equal(x, f) }) {
		return list
	}
	return append(list, bytes.Clone(f))
}

// active: there is something to hide.
func (s *secretScrubber) active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.values) > 0
}

// redactedB64 is what a value's base64 becomes: as long, and base64 itself, so a payload around it still decodes.
func redactedB64(f []byte) []byte {
	out := bytes.Repeat([]byte("A"), len(f))
	for i, c := range f {
		if c == '=' {
			out[i] = '='
		}
	}
	return out
}

// bytes is b, raw bytes the node sends (a terminal's output), with every value and its base64 hidden.
func (s *secretScrubber) bytes(b []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.raw {
		if bytes.Contains(b, f) {
			b = bytes.ReplaceAll(b, f, []byte(proto.Redacted))
		}
	}
	for _, f := range s.b64 {
		if bytes.Contains(b, f) {
			b = bytes.ReplaceAll(b, f, redactedB64(f))
		}
	}
	return b
}

// line is one JSON line with the values hidden inside its string values, never in object keys: a match that starts
// inside an escape sequence or crosses a string's end is left, so the line stays valid JSON. A value's base64 becomes
// as many base64 characters, so a base64 payload stays one (its bytes there garbled).
func (s *secretScrubber) line(b []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hit := false
	for _, l := range [][][]byte{s.json, s.b64} {
		for _, f := range l {
			if bytes.Contains(b, f) {
				hit = true
				break
			}
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
		end := i + 1 // the string's closing quote
		for end < len(b) && b[end] != '"' {
			if b[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(b) { // not closed: copied as it is
			out.Write(b[i:])
			break
		}
		str := b[i+1 : end]
		if isKey(b, end+1) {
			out.Write(b[i : end+1])
		} else {
			out.WriteByte('"')
			out.Write(s.inString(str))
			out.WriteByte('"')
		}
		i = end + 1
	}
	return out.Bytes()
}

// isKey: what follows a string's closing quote (at i) makes it an object key.
func isKey(b []byte, i int) bool {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	return i < len(b) && b[i] == ':'
}

// inString replaces the values in str, the inside of a JSON string, where a character or an escape starts only.
func (s *secretScrubber) inString(str []byte) []byte {
	var out []byte
	i, last := 0, 0
	for i < len(str) {
		matched, with := 0, []byte(nil)
		for _, f := range s.json {
			if bytes.HasPrefix(str[i:], f) {
				matched, with = len(f), []byte(proto.Redacted)
				break
			}
		}
		if matched == 0 {
			for _, f := range s.b64 {
				if bytes.HasPrefix(str[i:], f) {
					matched, with = len(f), redactedB64(f)
					break
				}
			}
		}
		if matched > 0 {
			out = append(out, str[last:i]...)
			out = append(out, with...)
			i += matched
			last = i
			continue
		}
		switch {
		case str[i] == '\\' && i+1 < len(str) && str[i+1] == 'u': // \uXXXX is one unit
			i += 6
		case str[i] == '\\':
			i += 2
		default:
			i++
		}
	}
	if out == nil {
		return str
	}
	return append(out, str[min(last, len(str)):]...)
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

// scrubBytes is b, raw bytes the node sends as base64 (the full herdr client's), with the handed-over values hidden.
// A value split between two pieces is not found.
func (n *Node) scrubBytes(b []byte) []byte {
	s := n.handoffs().scrub
	if !s.active() {
		return b
	}
	return s.bytes(bytes.Clone(b))
}

// scrubB64 is a base64 payload the node sends (a terminal frame's data) with the values hidden in the bytes it holds:
// decoded, hidden, encoded again. One that does not decode goes as it is (the connection's own hiding still sees it).
func (n *Node) scrubB64(data string) string {
	s := n.handoffs().scrub
	if !s.active() || data == "" {
		return data
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return data
	}
	clean := s.bytes(raw)
	if bytes.Equal(clean, raw) {
		return data
	}
	return base64.StdEncoding.EncodeToString(clean)
}

// ---- the start ----

const (
	secretEnvAgentCode = "secret_env_agent"
	secretEnvGoneCode  = "secret_env_gone"
	secretEnvAgentText = "secrets can be handed only to a new Claude Code start in a terminal, with a provider added in Persona or plain claude"
)

// secretStartProblem is why a start cannot take ticket: only a new Claude Code start whose command personad can exec
// (a Claude provider added in Persona, or plain claude; not a function from the shell's rc file), with the ticket
// staged; the start that passes takes the ticket, so no other start may name it.
func (n *Node) secretStartProblem(kind, launcher, resume, ticket string) error {
	if kind != "claude" || resume != "" {
		return refusal(secretEnvAgentCode, secretEnvAgentText, nil)
	}
	if l, managed := n.managedLauncher(launcher); managed && l.AgentType() != "claude" || !managed && launcher != "claude" {
		return refusal(secretEnvAgentCode, secretEnvAgentText, nil)
	}
	if !n.handoffs().reserve(ticket) {
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
