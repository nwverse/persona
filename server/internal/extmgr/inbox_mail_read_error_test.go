package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/mail"
)

// brokenMail is slowMail whose accounts in errs fail with their own error. An account in refuse shows in the
// accounts' list as refusing its login (Status "error", as the mail service's health says) once a read of it failed,
// the way a password changed elsewhere first shows up in a round.
type brokenMail struct {
	slowMail
	errs   map[string]error
	refuse map[string]bool
	mu     sync.Mutex
	tried  map[string]bool
}

func (m *brokenMail) ListAccounts() ([]mail.MailAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mail.MailAccount, 0, len(m.accounts))
	for _, a := range m.accounts {
		a.Status = "ok"
		if m.refuse[a.ID] && m.tried[a.ID] {
			a.Status, a.Error = "error", "the server refused the login"
		}
		out = append(out, a)
	}
	return out, nil
}

func (m *brokenMail) ListMessages(ctx context.Context, opts mail.ListOptions) (*mail.ListResult, error) {
	if err := m.errs[opts.AccountID]; err != nil {
		m.mu.Lock()
		if m.tried == nil {
			m.tried = map[string]bool{}
		}
		m.tried[opts.AccountID] = true
		m.mu.Unlock()
		return nil, err
	}
	return m.slowMail.ListMessages(ctx, opts)
}

// mailRoundWire is a round's mail as the wire carries it to the assistant.
type mailRoundWire struct {
	Mail *struct {
		Accounts []struct {
			ID string `json:"id"`
		} `json:"accounts"`
		Failed []struct {
			ID        string `json:"id"`
			Email     string `json:"email"`
			ReadError string `json:"read_error"`
		} `json:"failed"`
	} `json:"mail"`
}

// A mail account a full round could not read is named in the round's mail with a short code, as a connector's
// failed Collect is (#1849): it used to be only logged and left out, so the inbox showed its last mail as current and
// nothing said it was behind. A timeout is "timeout"; an account the mail settings show as refusing its login is
// "login_refused"; anything else "failed". The error's words never go on the wire. The account stays out of
// accounts, so an assistant from before this reads the round as it always did.
func TestMailRoundSaysWhichAccountCouldNotBeRead(t *testing.T) {
	m := &brokenMail{slowMail: slowMail{accounts: []mail.MailAccount{
		{ID: "a", Email: "a@example.com"}, {ID: "b", Email: "b@example.com"}, {ID: "c", Email: "c@example.com"},
		{ID: "d", Email: "d@example.com"}, {ID: "e", Email: "e@example.com"},
	}}, errs: map[string]error{
		"b": context.DeadlineExceeded,
		"c": &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded},
		"d": errors.New("邮箱服务器拒绝了登录：user me@example.com, password hunter2"),
		"e": errors.New("select INBOX: imaps://me:hunter2@imap.example.com: broken pipe"),
	}, refuse: map[string]bool{"d": true}}
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	h.mail = m
	if err := h.addConnector(mailConnector{h: h}); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(h.readInbox(context.Background(), roundFull))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Fatalf("an error's words went on the wire: %s", raw)
	}
	var r mailRoundWire
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Mail == nil {
		t.Fatalf("no mail in a full round: %s", raw)
	}
	var read []string
	for _, a := range r.Mail.Accounts {
		read = append(read, a.ID)
	}
	if join(read) != "a" {
		t.Fatalf("accounts = %s, want only the one read (an older assistant reads these as before)", join(read))
	}
	want := map[string]string{"b": "timeout", "c": "timeout", "d": "login_refused", "e": "failed"}
	got := map[string]string{}
	for _, f := range r.Mail.Failed {
		if f.Email != f.ID+"@example.com" {
			t.Fatalf("failed account %s has the address %q", f.ID, f.Email)
		}
		got[f.ID] = f.ReadError
	}
	if len(got) != len(want) {
		t.Fatalf("failed accounts = %v, want %v (round: %s)", got, want, raw)
	}
	for id, code := range want {
		if got[id] != code {
			t.Fatalf("account %s: read_error %q, want %q (round: %s)", id, got[id], code, raw)
		}
	}
}
