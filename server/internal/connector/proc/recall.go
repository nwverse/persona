package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connector/serve"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Taking back what the program sent (connector.Recaller, docs/extensions/connectors.md "Sent as the user"): a program whose manifest
// declares recall is asked with connector.recall, once the user agreed. It is a Recaller only by that declaration
// (connector.RecallerOf), and each method checks it again: this type implements every interface.

var _ connector.Recaller = (*Conn)(nil)

// recallWordsMost is how much of a program's own words a refusal keeps.
const recallWordsMost = 200

// CanRecall answers from the manifest alone, as it must (a page of the ledger asks it for every row): a program that
// declares recall can be asked about any action; the program says which it cannot (not_recallable). How late is the hub's
// own limit (sent_recall_hours), and the program's own is its to judge when it is asked.
func (c *Conn) CanRecall(string, time.Duration) bool { return c.Manifest().Has(connector.CapRecall) }

// Recall asks the program to take the message back and returns nil only when it says, recalled true, that the service
// confirmed it is gone. What it answers is mapped as connector.Recaller says: see recallFailure.
func (c *Conn) Recall(ctx context.Context, m connector.SentMessage) error {
	if !c.Manifest().Has(connector.CapRecall) {
		return fmt.Errorf("%w: %s does not say it can take a message back", connector.ErrCannotRecall, c.cfg.Name)
	}
	// A row with no time is before any install time, so it is refused too: it cannot be shown to be this install's.
	if nb := c.cfg.RecallNotBefore; !nb.IsZero() && m.At.Before(nb) {
		return fmt.Errorf("%w: the send is older than this install of %s", connector.ErrCannotRecall, c.cfg.Name)
	}
	var target map[string]any
	if len(m.Target) > 0 {
		_ = json.Unmarshal(m.Target, &target) // a target that is not an object goes as none
	}
	var out serve.RecallResult
	if err := c.call(ctx, serve.MRecall, serve.RecallParams{Action: m.Action, MessageRef: m.Ref, Target: target, At: m.At}, &out); err != nil {
		return recallFailure(c.cfg.Name, err)
	}
	if !out.Recalled {
		return fmt.Errorf("%w: %s did not say it was taken back", connector.ErrRecallUnknown, c.cfg.Name)
	}
	return nil
}

// recallFailure is what a failed call of connector.recall means to connector.Recaller: nothing was sent when the
// program is not running, cannot take that message back or cannot act now; the outcome is unknown when the request went
// out and no answer came (a connection that dropped, a deadline) or the program says so; anything else the program
// answers is the service refusing, in the program's words cut to one sentence.
func recallFailure(name string, err error) error {
	var re *rpc.Error
	switch {
	case errors.Is(err, errNotRunning), errors.Is(err, host.ErrNotRunning):
		return fmt.Errorf("%w: %s is not running", connector.ErrRecallUnavailable, name)
	case errors.As(err, &re):
		words := recallWords(name, re.Message)
		switch re.Code {
		case serve.CodeNotRecallable, serve.CodeUnknownMethod, serve.CodeNoCapability:
			return fmt.Errorf("%w: %s", connector.ErrCannotRecall, words)
		case serve.CodeNotSignedIn, serve.CodeOffline, serve.CodeRateLimited:
			return fmt.Errorf("%w: %s", connector.ErrRecallUnavailable, words)
		case serve.CodeRecallUnknown, extension.CodeTimeout:
			return fmt.Errorf("%w: %s", connector.ErrRecallUnknown, words)
		}
		return errors.New(words)
	}
	// No answer: the request may have reached the program (the connection dropped, the deadline passed, or what was
	// written or read half way failed).
	return fmt.Errorf("%w: no answer from %s", connector.ErrRecallUnknown, name)
}

// recallWords is a program's error as one sentence: its first line, cut; a program that said nothing is named.
func recallWords(name, message string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	if r := []rune(line); len(r) > recallWordsMost {
		line = string(r[:recallWordsMost])
	}
	if line = strings.TrimSpace(line); line == "" {
		return name + " did not take it back"
	}
	return line
}
