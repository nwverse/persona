package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- partner hubs' messages (partner_messages, partner_prefs; docs/server/partner-hubs.md) ----

// PartnerMessage is one envelope between this hub's assistant and a partner hub's, sent (Direction "out") or received
// ("in"), kept once per (Direction, PeerHub, MessageID).
type PartnerMessage struct {
	ID        string
	Direction string // out | in
	PeerHub   string // the partner's hub_id
	PeerName  string // its name when the row was written
	MessageID string // the envelope's message_id: the same on every resend
	Kind      string // message | reply | status
	ReplyTo   string // reply, status: the message it is about
	Hop       int
	// MachineID, SessionID and Title are the conversation: the one that sent it (out), the one it went into (in).
	MachineID string
	SessionID string
	Title     string
	// Text is a message's or a reply's text, scrubbed; for a status, the state it reports (delivered | failed).
	Text     string
	State    string
	Code     string // why it failed; a status: the code it reports
	CardID   string // the owner's confirmation card raised last
	Attempts int
	NextTry  *time.Time // when the delivery loop looks at it next; nil: nothing to do
	// PairedAt is the pairing it was written for (the hub's PartnerHub.PairedAt then; zero: not known): kept from the
	// hub's clock, so it is only ever compared with the hub's own pairing times.
	PairedAt  time.Time
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PartnerPref is the owner's setting for one partner hub: whether a message to it waits for their OK. PairedAt is the
// pairing it was made for (the hub's PartnerHub.PairedAt; zero: not known): it does not hold for a later pairing under
// the same hub_id.
type PartnerPref struct {
	HubID     string
	Confirm   bool
	PairedAt  time.Time
	UpdatedAt time.Time
}

const partnerMessageColumns = `id, direction, peer_hub, peer_name, message_id, kind, reply_to, hop, machine_id, session_id, title,
	text, state, code, card_id, attempts, next_try, paired_at, revision, created_at, updated_at`

func scanPartnerMessage(r pgx.Row) (*PartnerMessage, error) {
	m := new(PartnerMessage)
	var paired *time.Time
	err := r.Scan(&m.ID, &m.Direction, &m.PeerHub, &m.PeerName, &m.MessageID, &m.Kind, &m.ReplyTo, &m.Hop, &m.MachineID,
		&m.SessionID, &m.Title, &m.Text, &m.State, &m.Code, &m.CardID, &m.Attempts, &m.NextTry, &paired, &m.Revision,
		&m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if paired != nil {
		m.PairedAt = paired.UTC()
	}
	return m, err
}

// nullTime is t for a nullable column: NULL when zero.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

func scanPartnerMessages(rows pgx.Rows, err error) ([]PartnerMessage, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PartnerMessage
	for rows.Next() {
		m, err := scanPartnerMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// InsertPartnerMessage keeps m unless the same envelope (direction, peer, message_id) is kept already: then it returns
// that one and false. A zero CreatedAt is now.
func (d *DB) InsertPartnerMessage(ctx context.Context, m PartnerMessage) (PartnerMessage, bool, error) {
	created := m.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	row, err := scanPartnerMessage(d.pool.QueryRow(ctx, `INSERT INTO partner_messages (id, direction, peer_hub, peer_name,
		message_id, kind, reply_to, hop, machine_id, session_id, title, text, state, code, card_id, attempts, next_try,
		paired_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19)
		ON CONFLICT (direction, peer_hub, message_id) DO NOTHING RETURNING `+partnerMessageColumns,
		m.ID, m.Direction, m.PeerHub, m.PeerName, m.MessageID, m.Kind, m.ReplyTo, m.Hop, m.MachineID, m.SessionID, m.Title,
		m.Text, m.State, m.Code, m.CardID, m.Attempts, m.NextTry, nullTime(m.PairedAt), created.UTC()))
	if err != nil {
		return PartnerMessage{}, false, err
	}
	if row != nil {
		return *row, true, nil
	}
	had, err := d.PartnerMessageOf(ctx, m.Direction, m.PeerHub, m.MessageID)
	if err != nil {
		return PartnerMessage{}, false, err
	}
	if had == nil {
		return PartnerMessage{}, false, errors.New("partner message: conflicting row went away")
	}
	return *had, false, nil
}

// PartnerMessageOf is the envelope direction / peer / messageID; nil when there is none.
func (d *DB) PartnerMessageOf(ctx context.Context, direction, peer, messageID string) (*PartnerMessage, error) {
	return scanPartnerMessage(d.pool.QueryRow(ctx, `SELECT `+partnerMessageColumns+` FROM partner_messages
		WHERE direction=$1 AND peer_hub=$2 AND message_id=$3`, direction, peer, messageID))
}

// UpdatePartnerMessage writes m's state, code, card, attempts, next try and conversation if the row is still at
// m.Revision (then it is at m.Revision+1); false when another writer changed it first.
func (d *DB) UpdatePartnerMessage(ctx context.Context, m PartnerMessage) (bool, error) {
	res, err := d.pool.Exec(ctx, `UPDATE partner_messages SET state=$3, code=$4, card_id=$5, attempts=$6, next_try=$7,
		machine_id=$8, session_id=$9, title=$10, revision=revision+1, updated_at=now()
		WHERE id=$1 AND revision=$2`, m.ID, m.Revision, m.State, m.Code, m.CardID, m.Attempts, m.NextTry, m.MachineID,
		m.SessionID, m.Title)
	return res.RowsAffected() == 1, err
}

// ClaimPartnerMessages takes up to limit envelopes whose next try is due, for lease: their next try moves on by it
// (and their revision by one), so another manager on the same database skips them until it ends.
func (d *DB) ClaimPartnerMessages(ctx context.Context, lease time.Duration, limit int) ([]PartnerMessage, error) {
	return scanPartnerMessages(d.pool.Query(ctx, `UPDATE partner_messages SET next_try=now()+make_interval(secs => $1), revision=revision+1
		WHERE id IN (SELECT id FROM partner_messages WHERE next_try IS NOT NULL AND next_try<=now()
			ORDER BY next_try LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING `+partnerMessageColumns, lease.Seconds(), limit))
}

// PartnerMessagesInState are the envelopes of direction in state, oldest first.
func (d *DB) PartnerMessagesInState(ctx context.Context, direction, state string) ([]PartnerMessage, error) {
	return scanPartnerMessages(d.pool.Query(ctx, `SELECT `+partnerMessageColumns+` FROM partner_messages
		WHERE direction=$1 AND state=$2 ORDER BY created_at, id`, direction, state))
}

// RecentPartnerMessages are the latest limit messages and replies (no statuses) to and from peer ("": every partner),
// newest first.
func (d *DB) RecentPartnerMessages(ctx context.Context, peer string, limit int) ([]PartnerMessage, error) {
	return scanPartnerMessages(d.pool.Query(ctx, `SELECT `+partnerMessageColumns+` FROM partner_messages
		WHERE ($1='' OR peer_hub=$1) AND kind<>'status' ORDER BY created_at DESC, id DESC LIMIT $2`, peer, limit))
}

// CountPartnerMessages counts the envelopes of direction to or from peer written since since: the statuses (status),
// else the messages and replies.
func (d *DB) CountPartnerMessages(ctx context.Context, direction, peer string, status bool, since time.Time) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM partner_messages WHERE direction=$1 AND peer_hub=$2
		AND (kind='status')=$3 AND created_at>=$4`, direction, peer, status, since.UTC()).Scan(&n)
	return n, err
}

// PartnerPrefs are the partner hubs the owner set "ask me before sending" for, each with the pairing it was set for; a
// hub with none (or with one of an earlier pairing) asks.
func (d *DB) PartnerPrefs(ctx context.Context) ([]PartnerPref, error) {
	rows, err := d.pool.Query(ctx, `SELECT hub_id, confirm, paired_at, updated_at FROM partner_prefs ORDER BY hub_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PartnerPref
	for rows.Next() {
		var p PartnerPref
		var paired *time.Time
		if err := rows.Scan(&p.HubID, &p.Confirm, &paired, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if paired != nil {
			p.PairedAt = paired.UTC()
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPartnerConfirm keeps whether a message to hub waits for the owner's OK, for its pairing made at pairedAt (zero:
// not known), in place of a setting of an earlier pairing.
func (d *DB) SetPartnerConfirm(ctx context.Context, hub string, confirm bool, pairedAt time.Time) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO partner_prefs (hub_id, confirm, paired_at, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (hub_id) DO UPDATE SET confirm=EXCLUDED.confirm, paired_at=EXCLUDED.paired_at, updated_at=now()`,
		hub, confirm, nullTime(pairedAt))
	return err
}
