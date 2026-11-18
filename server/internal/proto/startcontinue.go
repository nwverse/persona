package proto

// Answering a start's startup question from the app (#10365, docs/protocol/protocol.md "Answering a start's
// question"): a New Agent start whose new tab stopped on a question of the shell's startup files (CodeShellWaiting)
// lists the question and the answers it may be given in the session's start_error_args; a full device sends one, the
// node types it into the kept tab while the question is still there and starts the session again in that tab.

// MethodStartContinue is the node method (capability nodeproto.StartContinue): StartContinueParams → {"session": Session}.
const MethodStartContinue = "start.continue"

// StartContinueParams is start.continue's params and the body of POST /api/v1/sessions/{mid}/{sid}/start/continue
// (the route's sid is SessionID).
type StartContinueParams struct {
	SessionID string `json:"session_id,omitempty"`
	// Choice is one of start_error_args.choices: a key ("y", "n", "q", "0", "c"), "enter", "yes", "no" or "cancel"
	// (Ctrl-C). Nothing else is typed.
	Choice string `json:"choice"`
}

// StartErrorArgs keys of a CodeShellWaiting start (besides line, screen, pane and kept_until): the question's kind
// (update, yes_no, press_key, compinit, ssh_host_key, zsh_newuser, p10k_wizard, rvm_trust, secret), its answers' ids
// joined by commas, "true" when it reads a whole line or a secret, and the answer Enter alone gives.
const (
	ShellAskKind     = "kind"
	ShellAskChoices  = "choices"
	ShellAskLineMode = "line_mode"
	ShellAskSecret   = "secret"
	ShellAskDefault  = "default"
)
