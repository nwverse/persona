package larkcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
)

const defaultTimeout = 15 * time.Second

// Config controls the executable and profile. It contains no credentials.
type Config struct {
	Bin     string
	Profile string
	Timeout time.Duration
	// InboxPoll and InboxPageLimit are consumed by the built-in larkcli
	// connector. Keeping them here lets tests and embedders construct the
	// adapter and connector together without another configuration object.
	InboxPoll      time.Duration
	InboxPageLimit int
	// Now is injectable for expiry tests; production uses time.Now.
	Now func() time.Time
}

// Runner executes lark-cli with a bounded context and decodes only JSON output.
// It never logs stdout or stderr. The latter is captured solely to classify a
// structured CLI error without retaining its message body in the returned error.
type Runner struct {
	cfg Config

	mu   sync.Mutex
	path string
}

// New creates a runner. An empty Bin means lark-cli on PATH (or in the login
// shell's PATH, matching the L1 assistant wrapper).
func New(cfg Config) *Runner {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Runner{cfg: cfg}
}

// NewRunner is a compact constructor for callers that already have the three
// configuration values from lark.cli.
func NewRunner(bin, profile string, timeout time.Duration) *Runner {
	return New(Config{Bin: bin, Profile: profile, Timeout: timeout})
}

// Profile returns the configured lark-cli profile name. It contains no
// credentials and is used only to make user-facing commands point at the same
// profile as the runner.
func (r *Runner) Profile() string {
	if r == nil {
		return ""
	}
	return r.cfg.Profile
}

// Run executes a read-only user command and returns its JSON stdout. It is the
// generic escape hatch for adapter additions; typed methods below own the L2
// command shapes.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.execute(ctx, args, outputJSON)
}

// RunJSON is an explicit alias for Run for callers that want to document the
// output contract at the call site.
func (r *Runner) RunJSON(ctx context.Context, args ...string) ([]byte, error) {
	return r.Run(ctx, args...)
}

// RunAs executes a read or write command with an explicit lark-cli identity.
// Reads in this adapter use user by default; Inbox replies deliberately use bot
// because the default Lark app cannot send as the user.
func (r *Runner) RunAs(ctx context.Context, identity string, args ...string) ([]byte, error) {
	if identity == "" {
		identity = "user"
	}
	return r.executeAs(ctx, args, outputJSON, identity)
}

func (r *Runner) now() time.Time {
	if r != nil && r.cfg.Now != nil {
		return r.cfg.Now()
	}
	return time.Now()
}

// Version returns the executable's semantic version as printed by --version.
func (r *Runner) Version(ctx context.Context) (string, error) {
	out, err := r.execute(ctx, []string{"--version"}, outputVersion)
	if err != nil {
		return "", err
	}
	version := parseVersion(string(out))
	if version == "" {
		return "", cliError(KindOther, errors.New("invalid version output"))
	}
	return version, nil
}

// CheckVersion rejects versions older than the pinned minimum.
func (r *Runner) CheckVersion(ctx context.Context) error {
	got, err := r.Version(ctx)
	if err != nil {
		return err
	}
	if compareVersion(got, MinimumVersion) < 0 {
		return &Error{Kind: KindUnsupportedVersion, Version: got, Minimum: MinimumVersion}
	}
	return nil
}

// AuthStatus reads the user identity without touching credentials or changing
// lark-cli configuration.
func (r *Runner) AuthStatus(ctx context.Context) (AuthStatus, error) {
	out, err := r.execute(ctx, []string{"auth", "status"}, outputAuth)
	if err != nil {
		return AuthStatus{}, err
	}
	var raw authStatusWire
	if err := json.Unmarshal(out, &raw); err != nil {
		return AuthStatus{}, cliError(KindOther, errors.New("invalid auth status JSON"))
	}
	return raw.result(r.now()), nil
}

// ListChats calls the recent chat shortcut. It includes both group and direct
// chats and asks the CLI for newest activity first.
func (r *Runner) ListChats(ctx context.Context, limit int) (ChatList, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	out, err := r.execute(ctx, []string{"im", "+chat-list", "--types", "p2p,group", "--sort", "active_time", "--page-size", fmt.Sprint(limit)}, outputJSON)
	if err != nil {
		return ChatList{}, err
	}
	var page ChatList
	if err := decodeData(out, &page); err != nil {
		return ChatList{}, cliError(KindOther, errors.New("invalid chat list JSON"))
	}
	return page, nil
}

// ListChatsAtMe finds chats with a message mentioning the user and normalizes
// the message results to the same chat list shape as ListChats.
func (r *Runner) ListChatsAtMe(ctx context.Context, limit int) (ChatList, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	page, err := r.SearchMessages(ctx, "", SearchOptions{AtMe: true, Limit: limit})
	if err != nil {
		return ChatList{}, err
	}
	return ChatListFromMessages(page.Messages, limit), nil
}

// ChatListFromMessages keeps one row per conversation while preserving the
// first result's activity order.
func ChatListFromMessages(messages []Message, limit int) ChatList {
	seen := map[string]bool{}
	out := ChatList{}
	for _, message := range messages {
		if message.ChatID == "" || seen[message.ChatID] {
			continue
		}
		seen[message.ChatID] = true
		out.Chats = append(out.Chats, Chat{ChatID: message.ChatID, Name: message.ChatName, ChatType: message.ChatType})
		if len(out.Chats) == limit {
			break
		}
	}
	out.Total = len(out.Chats)
	return out
}

// ListMessages reads one chat or thread. The CLI's newest-first response is
// reversed so the connector's historical tool contract remains oldest first.
func (r *Runner) ListMessages(ctx context.Context, chatID, thread, before string, limit int) (MessagePage, error) {
	if limit <= 0 {
		limit = 40
	}
	if limit > 50 {
		limit = 50
	}
	args := []string{"im"}
	if thread != "" {
		args = append(args, "+threads-messages-list", "--thread", thread)
	} else {
		if chatID == "" {
			return MessagePage{}, errors.New("chat_id is required")
		}
		args = append(args, "+chat-messages-list", "--chat-id", chatID)
	}
	args = append(args, "--order", "desc", "--page-size", fmt.Sprint(limit), "--no-reactions")
	if before != "" {
		args = append(args, "--page-token", before)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return MessagePage{}, err
	}
	var page MessagePage
	if err := decodeData(out, &page); err != nil {
		return MessagePage{}, cliError(KindOther, errors.New("invalid message list JSON"))
	}
	for i, j := 0, len(page.Messages)-1; i < j; i, j = i+1, j-1 {
		page.Messages[i], page.Messages[j] = page.Messages[j], page.Messages[i]
	}
	return page, nil
}

// SearchMessages searches the user's messages. It deliberately disables
// reaction enrichment: reactions are an optional second permission and are not
// needed by the read-tool contract.
func (r *Runner) SearchMessages(ctx context.Context, query string, opts SearchOptions) (MessagePage, error) {
	args := []string{"im", "+messages-search"}
	if query != "" {
		args = append(args, "--query", query)
	}
	for _, pair := range [][2]string{{"--chat-id", opts.ChatID}, {"--sender", opts.Sender}, {"--chat-type", opts.ChatType}, {"--start", opts.Start}, {"--end", opts.End}} {
		if pair[1] != "" {
			args = append(args, pair[0], pair[1])
		}
	}
	if opts.AtMe {
		args = append(args, "--is-at-me")
	}
	args = append(args, "--page-size", fmt.Sprint(opts.normalizedLimit()), "--no-reactions")
	if opts.PageToken != "" {
		args = append(args, "--page-token", opts.PageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return MessagePage{}, err
	}
	var page MessagePage
	if err := decodeData(out, &page); err != nil {
		return MessagePage{}, cliError(KindOther, errors.New("invalid message search JSON"))
	}
	return page, nil
}

// Search runs the lark_search kind mappings that are safe and read-only in L2.
// Message search is typed; the other supported kinds are returned as the CLI's
// normalized JSON data because their schemas belong to later connector steps.
func (r *Runner) Search(ctx context.Context, kind, query string) (json.RawMessage, error) {
	switch kind {
	case "", "messages":
		page, err := r.SearchMessages(ctx, query, SearchOptions{})
		if err != nil {
			return nil, err
		}
		return json.Marshal(page)
	case "chats":
		return r.rawSearch(ctx, []string{"im", "+chat-search", "--query", query})
	case "docs":
		return r.rawSearch(ctx, []string{"drive", "+search", "--query", query})
	case "people":
		return r.rawSearch(ctx, []string{"contact", "+search-user", "--query", query})
	case "minutes":
		return r.rawSearch(ctx, []string{"minutes", "+search", "--query", query})
	case "todos":
		return r.rawSearch(ctx, []string{"task", "+search", "--query", query})
	case "mail":
		return r.rawSearch(ctx, []string{"mail", "+triage", "--query", query})
	case "calendar":
		return r.rawSearch(ctx, []string{"calendar", "+agenda"})
	default:
		return nil, fmt.Errorf("unknown search kind %q", kind)
	}
}

// SendText sends a plain text message as the bot identity. The idempotency key
// is optional and is passed through to lark-cli when supplied.
func (r *Runner) SendText(ctx context.Context, chatID, text, idempotencyKey string) (json.RawMessage, error) {
	if strings.TrimSpace(chatID) == "" {
		return nil, errors.New("chat_id is required")
	}
	if text == "" {
		return nil, errors.New("text is required")
	}
	args := []string{"im", "+messages-send", "--chat-id", chatID, "--text", text}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	return r.RunAs(ctx, "bot", args...)
}

// ReplyText replies to a known message as the bot identity. The caller may ask
// for a topic reply; normal Inbox replies keep the source message's topic.
func (r *Runner) ReplyText(ctx context.Context, messageID, text, idempotencyKey string, inThread bool) (json.RawMessage, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, errors.New("message_id is required")
	}
	if text == "" {
		return nil, errors.New("text is required")
	}
	args := []string{"im", "+messages-reply", "--message-id", messageID, "--text", text}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	if inThread {
		args = append(args, "--reply-in-thread")
	}
	return r.RunAs(ctx, "bot", args...)
}

func (r *Runner) rawSearch(ctx context.Context, args []string) (json.RawMessage, error) {
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return nil, err
	}
	data, err := unwrapData(out)
	if err != nil {
		return nil, cliError(KindOther, errors.New("invalid search JSON"))
	}
	return data, nil
}

type outputMode uint8

const (
	outputVersion outputMode = iota
	outputAuth
	outputJSON
)

type commandResult struct {
	stdout []byte
	stderr []byte
}

func (r *Runner) execute(ctx context.Context, args []string, mode outputMode) ([]byte, error) {
	identity := ""
	if mode == outputJSON {
		identity = "user"
	}
	return r.executeAs(ctx, args, mode, identity)
}

func (r *Runner) executeAs(ctx context.Context, args []string, mode outputMode, identity string) ([]byte, error) {
	bin, err := r.binary(ctx)
	if err != nil {
		return nil, err
	}
	argv := make([]string, 0, len(args)+5)
	if r.cfg.Profile != "" {
		argv = append(argv, "--profile", r.cfg.Profile)
	}
	argv = append(argv, args...)
	switch mode {
	case outputAuth:
		argv = append(argv, "--json")
	case outputJSON:
		if identity == "" {
			identity = "user"
		}
		argv = append(argv, "--as", identity, "--format", "json")
	}

	timeout := r.cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, argv...)
	cmd.Env = cliEnv(bin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	result := commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if cctx.Err() != nil {
		return nil, &Error{Kind: KindTimeout, cause: cctx.Err()}
	}
	if err != nil {
		if classified := classifyFailure(result); classified != nil {
			return nil, classified
		}
		return nil, cliError(KindOther, err)
	}
	return result.stdout, nil
}

func (r *Runner) binary(ctx context.Context) (string, error) {
	r.mu.Lock()
	if r.path != "" {
		path := r.path
		r.mu.Unlock()
		return path, nil
	}
	r.mu.Unlock()
	bin := r.cfg.Bin
	if bin == "" {
		bin = "lark-cli"
	}
	if filepath.IsAbs(bin) {
		if _, err := os.Stat(bin); err != nil {
			return "", cliError(KindNotInstalled, err)
		}
		r.mu.Lock()
		r.path = bin
		r.mu.Unlock()
		return bin, nil
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		// npm global installs are often visible only in an interactive login shell.
		lookupCtx, cancel := context.WithTimeout(ctx, minDuration(r.cfg.Timeout, 5*time.Second))
		defer cancel()
		cmd := exec.CommandContext(lookupCtx, loginShell(), "-lc", "command -v "+shellQuote(bin))
		out, _ := cmd.Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 && filepath.IsAbs(strings.TrimSpace(lines[len(lines)-1])) {
			path = strings.TrimSpace(lines[len(lines)-1])
		}
	}
	if path == "" {
		return "", cliError(KindNotInstalled, err)
	}
	r.mu.Lock()
	r.path = path
	r.mu.Unlock()
	return path, nil
}

func cliEnv(bin string) []string {
	return append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func loginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 || a > b {
		return b
	}
	return a
}

var versionPattern = lazyre.New(`(?i)(?:lark-cli\s+version\s+|version\s+)([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)`)

func parseVersion(s string) string {
	match := versionPattern.FindStringSubmatch(s)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func compareVersion(a, b string) int {
	parse := func(s string) [3]int {
		var v [3]int
		base := strings.SplitN(s, "-", 2)[0]
		parts := strings.Split(base, ".")
		for i := 0; i < len(parts) && i < len(v); i++ {
			_, _ = fmt.Sscan(parts[i], &v[i])
		}
		return v
	}
	va, vb := parse(a), parse(b)
	for i := range va {
		if va[i] < vb[i] {
			return -1
		}
		if va[i] > vb[i] {
			return 1
		}
	}
	return 0
}

func decodeData(raw []byte, out any) error {
	data, err := unwrapData(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func unwrapData(raw []byte) (json.RawMessage, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if data, ok := env["data"]; ok && len(data) > 0 && string(data) != "null" {
		return data, nil
	}
	return raw, nil
}

func classifyFailure(result commandResult) *Error {
	for _, raw := range [][]byte{result.stdout, result.stderr} {
		if len(raw) == 0 {
			continue
		}
		var env struct {
			Error struct {
				Type           string   `json:"type"`
				Subtype        string   `json:"subtype"`
				Message        string   `json:"message"`
				MissingScopes  []string `json:"missing_scopes"`
				MissingScopes2 []string `json:"missingScopes"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Error.Type == "" && env.Error.Subtype == "" {
			continue
		}
		sub := strings.ToLower(env.Error.Subtype)
		typ := strings.ToLower(env.Error.Type)
		scopes := append(env.Error.MissingScopes, env.Error.MissingScopes2...)
		if len(scopes) == 0 {
			scopes = scopesFromMessage(env.Error.Message)
		}
		switch {
		case sub == "missing_scope" || sub == "permission_denied" || sub == "forbidden" || typ == "permission":
			return &Error{Kind: KindMissingPermission, MissingScopes: scopes}
		case sub == "rate_limited" || sub == "too_many_requests" || typ == "rate_limit":
			return &Error{Kind: KindRateLimited}
		case sub == "token_expired" || sub == "expired" || sub == "not_logged_in" || sub == "unauthorized" || typ == "authentication":
			if sub == "token_expired" || sub == "expired" {
				return cliError(KindExpired, nil)
			}
			return cliError(KindNotLoggedIn, nil)
		}
	}
	text := strings.ToLower(string(result.stdout) + "\n" + string(result.stderr))
	switch {
	case strings.Contains(text, "missing scope") || strings.Contains(text, "missing_scope"):
		return &Error{Kind: KindMissingPermission, MissingScopes: scopesFromMessage(text)}
	case strings.Contains(text, "rate limit") || strings.Contains(text, "too many requests"):
		return &Error{Kind: KindRateLimited}
	case strings.Contains(text, "token expired") || strings.Contains(text, "expired"):
		return &Error{Kind: KindExpired}
	case strings.Contains(text, "not logged in") || strings.Contains(text, "not signed in"):
		return &Error{Kind: KindNotLoggedIn}
	}
	return nil
}

func scopesFromMessage(message string) []string {
	lower := strings.ToLower(message)
	const marker = "missing required scope(s):"
	index := strings.Index(lower, marker)
	if index < 0 {
		return nil
	}
	value := message[index+len(marker):]
	if end := strings.IndexAny(value, "\r\n"); end >= 0 {
		value = value[:end]
	}
	if end := strings.IndexAny(value, "\"}"); end >= 0 {
		value = value[:end]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	return uniqueStrings(parts)
}
