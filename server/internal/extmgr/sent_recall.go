package extmgr

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
)

// Taking back what was sent as the user (docs/extensions/connectors.md "Sent as the user", #5278). A row of the ledger can be taken
// back when the connector that wrote it can (connector.Recaller), it said which message it made, the message is recent
// enough and it was not taken back before. RecallSent is what the route calls: the checks, the connector's recall,
// and only when the other side says the message is gone, recalled_at. Who may ask, and whether the user is asked first
// (a card, their own device), is the caller's: it passes how the user agreed.

// recallStore reads one row of the ledger and marks it recalled (db.SentAsUserByID…).
type recallStore interface {
	SentAsUserByID(ctx context.Context, id int64) (db.SentAsUser, bool, error)
	MarkSentAsUserRecalled(ctx context.Context, id int64, at time.Time) (bool, error)
}

var _ recallStore = (*db.DB)(nil)

const (
	sentRecallHours = 24             // how long after a send it can be taken back unless manager.json sent_recall_hours says otherwise
	sentRecallMost  = 100 * 365 * 24 // the most hours it counts (a hundred years): a time.Duration of more would wrap around
)

// sentRecallTimeout is how long the connector has for its one recall; a variable so that tests can shorten it.
var sentRecallTimeout = 30 * time.Second

// Why a recall did not happen (RecallError.Code): the words the route answers with.
const (
	RecallNotFound        = "not_found"             // no such row
	RecallNotRecallable   = "not_recallable"        // its connector cannot take that message back, or the row has no way to find it
	RecallTooOld          = "too_old"               // sent longer ago than it can be taken back
	RecallAlreadyRecalled = "already_recalled"      // taken back before: the row comes with it
	RecallUnavailable     = "connector_unavailable" // its connector is switched off, not signed in or offline
	RecallRefused         = "recall_refused"        // the recall did not happen: the other side refused, or whether the message is gone could not be checked; nothing was changed
	RecallUnknown         = "recall_unknown"        // the request may have taken effect: nothing is recorded, and asking again is safe
	RecallNoDatabase      = "no_database"           // there is no ledger
)

// RecallError is why a recall did not happen. An error that is not one (a database that failed, a missing way to
// confirm) is the caller's to treat as a server error.
type RecallError struct {
	Code string
	Err  error
}

func (e *RecallError) Error() string { return e.Err.Error() }
func (e *RecallError) Unwrap() error { return e.Err }

func recallErr(code, why string) *RecallError { return &RecallError{Code: code, Err: errors.New(why)} }

// RecallConfirm is how the caller made sure the user agrees to take this message back: it runs once every check has
// passed and before the connector is asked, and an error from it stops the recall and is returned as it is.
type RecallConfirm func(ctx context.Context, row db.SentAsUser) error

// recallWindow is how long after a send it can be taken back (sent_recall_hours: 24 hours when omitted or 0, no limit
// below 0); the connector's own limit (CanRecall) counts as well, the stricter of the two.
func (h *Manager) recallWindow() (limit time.Duration, limited bool) {
	switch hours := h.cfg.SentRecallHours; {
	case hours < 0:
		return 0, false
	case hours == 0:
		return sentRecallHours * time.Hour, true
	default:
		return time.Duration(min(hours, sentRecallMost)) * time.Hour, true
	}
}

// recallCheck says why row's message cannot be taken back at now, nil and the connector that would do it when it can.
// It is everything but whether the other side agrees, and answers from memory, so a page of the ledger can say which of
// its rows offer a recall with the same answer RecallSent acts on.
func (h *Manager) recallCheck(ctx context.Context, row db.SentAsUser, now time.Time) (connector.Recaller, *RecallError) {
	if row.RecalledAt != nil {
		return nil, recallErr(RecallAlreadyRecalled, "this message was taken back already")
	}
	var c connector.Connector
	if h.connectors != nil {
		c = h.connectors.Get(row.Connector)
	}
	rc, ok := connector.RecallerOf(c)
	switch {
	case c == nil:
		return nil, recallErr(RecallNotRecallable, row.Connector+" is not here any more")
	case !ok:
		return nil, recallErr(RecallNotRecallable, c.Name()+" cannot take back what it sent")
	case row.MessageRef == "":
		return nil, recallErr(RecallNotRecallable, "the send did not say which message it made")
	case !rc.CanRecall(row.Action, 0):
		return nil, recallErr(RecallNotRecallable, c.Name()+" cannot take back this kind of message")
	}
	if rerr := h.recallOwnerCheck(c, row); rerr != nil {
		return nil, rerr
	}
	age := max(now.Sub(row.At), 0)
	if limit, limited := h.recallWindow(); (limited && age > limit) || !rc.CanRecall(row.Action, age) {
		return nil, recallErr(RecallTooOld, "this message was sent too long ago to be taken back")
	}
	if st := connector.StatusOf(ctx, c); st.State != connector.StateOK {
		return nil, recallErr(RecallUnavailable, c.Name()+" is not available: "+cmp.Or(st.Error, st.State))
	}
	return rc, nil
}

// RecallSent takes back the message the ledger row id made and marks the row recalled. The order is: the row, the
// checks (recallCheck), confirm, the connector's Recall, and only when that says the message is gone, recalled_at.
// A recall the connector refuses or cannot tell the outcome of marks nothing, and may be asked again. The row comes
// back whenever one was found, with recalled_at set when it was taken back.
//
// Nothing here says who may ask: the caller applies the ledger's own rule for reading it (a full device of the user's,
// or the owner; never an extension's token), and a recall started in a conversation or by an extension is the caller's
// to refuse. Two recalls of one row at once each call confirm: a caller that asks the user dedupes them.
//
// The age is judged when the request comes, not after confirm (a card can wait). The mark is written even when the
// request has gone away meanwhile, as the message is gone; two recalls of one row at once both reach the connector
// (which allows repeats) but only the first marks it, the other gets RecallAlreadyRecalled.
func (h *Manager) RecallSent(ctx context.Context, id int64, confirm RecallConfirm) (db.SentAsUser, error) {
	if confirm == nil {
		return db.SentAsUser{}, errors.New("a recall needs the caller to say how the user agreed")
	}
	st, _ := h.store.(recallStore)
	if st == nil {
		return db.SentAsUser{}, recallErr(RecallNoDatabase, "no database: there is no ledger")
	}
	row, found, err := st.SentAsUserByID(ctx, id)
	switch {
	case err != nil:
		return db.SentAsUser{}, err
	case !found:
		return db.SentAsUser{}, recallErr(RecallNotFound, "no such row in the ledger")
	}
	rc, rerr := h.recallCheck(ctx, row, sentLedgerNow())
	if rerr != nil {
		return row, rerr
	}
	if err := confirm(ctx, row); err != nil {
		return row, err
	}
	if err := ctx.Err(); err != nil { // the request went away while the user was asked: nothing was tried
		return row, err
	}
	cctx, cancel := context.WithTimeout(ctx, sentRecallTimeout)
	err = rc.Recall(cctx, connector.SentMessage{Action: row.Action, Ref: row.MessageRef, Target: row.Target, At: row.At})
	unfinished := cctx.Err() != nil // asked too long, or the request went away: the connector may not have heard how it ended
	cancel()
	if err != nil {
		code := RecallRefused // the other side said no, or could not check whether the message is gone: nothing changed
		switch {
		case unfinished || errors.Is(err, connector.ErrRecallUnknown):
			code = RecallUnknown
		case errors.Is(err, connector.ErrCannotRecall): // the connector found it could not, before it sent anything
			code = RecallNotRecallable
		case errors.Is(err, connector.ErrRecallUnavailable):
			code = RecallUnavailable
		}
		h.logf("sent ledger: recall of row %d (%s %s): %s", id, row.Connector, row.Action, code)
		return row, &RecallError{Code: code, Err: err}
	}
	at := sentLedgerNow()
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	marked, err := st.MarkSentAsUserRecalled(mctx, id, at)
	if err != nil {
		h.logf("sent ledger: row %d was taken back but could not be marked: %v", id, err)
		return row, errors.New("the message was taken back, but the ledger could not say so: ask again")
	}
	if !marked { // another recall of this row marked it first
		if again, found, err := st.SentAsUserByID(mctx, id); err == nil && found {
			row = again
		}
		return row, recallErr(RecallAlreadyRecalled, "this message was taken back already")
	}
	h.logf("sent ledger: recall of row %d (%s %s): done", id, row.Connector, row.Action)
	row.RecalledAt = &at
	return row, nil
}
