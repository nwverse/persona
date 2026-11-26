package assistantwire

import (
	"context"
	"time"
)

// LarkPushCopy tells Core when a push starts and when its first card lands (or a companion takes it).
// Both callbacks are optional and run before the push call returns.
type LarkPushCopy struct {
	Started   func()
	Delivered func()
}

// LarkReminder is one claimed to-do reminder. Copy rechecks Core's privacy decision before each
// attempt and supplies the permitted text. Current checks that the occurrence has not been changed.
type LarkReminder struct {
	Todo      string
	At        *time.Time
	Copy      func(context.Context) (title, body string, private bool)
	Current   func() bool
	Delivered func()
}

// LarkReminderPairing binds a reminder post to the owner and pairing generation it first attempted.
type LarkReminderPairing struct {
	Owner    string
	PairedAt time.Time
}

type larkReminderPairingKey struct{}

func WithLarkReminderPairing(ctx context.Context, pairing LarkReminderPairing) context.Context {
	return context.WithValue(ctx, larkReminderPairingKey{}, pairing)
}

func LarkReminderPairingOf(ctx context.Context) (LarkReminderPairing, bool) {
	p, ok := ctx.Value(larkReminderPairingKey{}).(LarkReminderPairing)
	return p, ok
}

// LarkMirror is where a turn or a prepared briefing card is sent. StillDue checks Core's live push
// ledger and the original turn before a retry, after the module has checked the bot's switches.
type LarkMirror struct {
	SessionRef
	Main     bool
	Label    string
	StillDue func(ctx context.Context, pushes bool) bool
}

// LarkMirrorTurn is the finished turn that Core cleared to mirror. Question is the short question
// that can appear on the card, while Asked and Turn are used for the delivery key.
type LarkMirrorTurn struct {
	Asked, Question, Answer, Turn string
}

// LarkAgentDone is the visible content and reply title of a finished Agent notice cleared by Core.
type LarkAgentDone struct {
	Head, Text, Place, Secondary, ThreadTitle string
	AwaitsReply                               bool
}
