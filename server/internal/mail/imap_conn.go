package mail

import (
	"context"
	"errors"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// conn is a cached, logged-in IMAP client.
type conn struct {
	c        *imapclient.Client
	lastUsed time.Time
}

// connProbe is when getConn checks a cached connection before handing it out: one unused for longer than after
// must answer a NOOP within wait, or it is dropped and dialed again. A NAT or firewall that silently forgot an idle
// flow would otherwise leave the next command hanging for the whole networkTimeout.
type connProbe struct {
	after, wait time.Duration
}

var defaultConnProbe = connProbe{after: time.Minute, wait: 5 * time.Second}

// probeLimits is s.probe with the defaults filled in (a zero Service has none).
func (s *Service) probeLimits() connProbe {
	p := s.probe
	if p.after <= 0 {
		p.after = defaultConnProbe.after
	}
	if p.wait <= 0 {
		p.wait = defaultConnProbe.wait
	}
	return p
}

// alive reports whether c answers a NOOP within wait. A connection that doesn't is closed, which also ends the NOOP.
func alive(ctx context.Context, c *imapclient.Client, wait time.Duration) bool {
	done := make(chan error, 1)
	go func() { done <- c.Noop().Wait() }()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case err := <-done:
		return err == nil
	case <-t.C:
	case <-ctx.Done():
	}
	c.Close()
	return false
}

// getConn returns a logged-in IMAP client for the account, dialing if needed. The caller holds the account's lock
// (withConnAt), so nothing else uses the connection while it is probed.
func (s *Service) getConn(ctx context.Context, a *MailAccount) (*imapclient.Client, error) {
	s.mu.Lock()
	cached := s.conns[a.ID]
	if cached != nil {
		if idle := s.now().Sub(cached.lastUsed); idle <= idleTimeout {
			cached.lastUsed = s.now()
			s.mu.Unlock()
			p := s.probeLimits()
			if idle <= p.after || alive(ctx, cached.c, p.wait) {
				return cached.c, nil
			}
			logf("mail: %s: the connection idle for %v did not answer a NOOP; reconnecting", a.ID, idle.Round(time.Second))
			if err := ctx.Err(); err != nil {
				s.dropConn(a.ID)
				return nil, err
			}
			s.mu.Lock()
		}
	}
	stale := s.conns[a.ID]
	delete(s.conns, a.ID)
	s.mu.Unlock()
	if stale != nil {
		stale.c.Close()
	}

	type result struct {
		c   *imapclient.Client
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := s.dialIMAP(context.Background(), a)
		ch <- result{c, err}
	}()
	var c *imapclient.Client
	select {
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		c = r.c
	}

	s.mu.Lock()
	// A concurrent caller may have installed a connection meanwhile; prefer it
	// and drop ours so we never leak.
	if existing, ok := s.conns[a.ID]; ok {
		s.mu.Unlock()
		c.Close()
		return existing.c, nil
	}
	s.conns[a.ID] = &conn{c: c, lastUsed: s.now()}
	s.mu.Unlock()
	return c, nil
}

// dropConn removes the cached connection for an account, closing it.
func (s *Service) dropConn(id string) {
	s.mu.Lock()
	c := s.conns[id]
	delete(s.conns, id)
	s.mu.Unlock()
	if c != nil {
		c.c.Close()
	}
}

// withConn runs fn on the cached connection; a transport-level error closes the
// connection and fn is retried once on a fresh one.
func (s *Service) withConn(ctx context.Context, a *MailAccount, fn func(*imapclient.Client) error) error {
	return s.withConnAt(ctx, a, func(c *imapclient.Client, _ time.Time) error { return fn(c) })
}

// withConnAt is withConn handing fn the operation's deadline: networkTimeout after the account was free (connecting
// included), or the context's if that is sooner, and the same one when it retries. Work done in steps (a page of a
// slow mailbox) stops in time with it.
func (s *Service) withConnAt(ctx context.Context, a *MailAccount, fn func(c *imapclient.Client, deadline time.Time) error) error {
	// IMAP is stateful (SELECT), so a cached connection can only serve one
	// command sequence at a time. The wait for the turn is the caller's ctx's:
	// a caller that gave up leaves the queue (#2344).
	mu := s.accountLock(a.ID)
	if err := mu.acquire(ctx); err != nil {
		return err
	}
	defer mu.release()

	// The operation's own time starts once the account is free, on purpose: a
	// slow mailbox ahead in the queue must not eat it.
	deadline := time.Now().Add(networkTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	run := func() error {
		c, err := s.getConn(ctx, a)
		s.noteLogin(a.ID, err)
		if err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- fn(c, deadline) }()
		select {
		case err := <-done:
			return err
		case <-time.After(time.Until(deadline)):
			s.dropConn(a.ID)
			return ErrTimedOut
		case <-ctx.Done():
			s.dropConn(a.ID)
			return ctx.Err()
		}
	}

	err := run()
	if err == nil || !isConnError(err) {
		return err
	}
	s.dropConn(a.ID)
	return run()
}

// isConnError reports whether err means the connection is unusable, so a
// reconnect is worth trying.
func isConnError(err error) bool {
	if err == nil {
		return false
	}
	var ie *imap.Error
	if errors.As(err, &ie) {
		// A tagged NO/BAD is a protocol answer, not a broken connection.
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}
