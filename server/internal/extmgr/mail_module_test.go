package extmgr

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/extmgr/mailport"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/mailwire"

	// The built-in modules, as the manager links them: the mail module provides itself to mailport.
	_ "github.com/nwverse/persona/server/internal/modules/all"
)

// needsMailModule skips a test of the mail module's behaviour when the build leaves it out (persona_without_mail,
// persona_core_only).
func needsMailModule(t *testing.T) mailwire.Factory {
	t.Helper()
	f := mailport.Factory()
	if f == nil {
		t.Skip("this build has no mail module (persona_without_mail or persona_core_only)")
	}
	return f
}

// openTestMail is the real mail module on dir (its own secret.key there) and rows, closed when the test ends.
func openTestMail(t *testing.T, dir string, rows mailwire.Store) Mail {
	t.Helper()
	s, err := needsMailModule(t)(mailwire.Config{DataDir: dir, SecretKey: filepath.Join(dir, "secret.key"), Store: rows})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := s.(io.Closer); ok {
		t.Cleanup(func() { c.Close() })
	}
	return s
}

// testMail is the real mail module with no accounts, on a directory of its own.
func testMail(t *testing.T) Mail {
	t.Helper()
	return openTestMail(t, t.TempDir(), &mailRows{})
}

// mailRows keeps a test's mail accounts in memory (the mail_accounts table) and no settings.
func (m *mailRows) Setting(context.Context, string, any) (bool, error) { return false, nil }
func (m *mailRows) PutSetting(context.Context, string, any) error      { return nil }

// unknownMailAccount stands for the mail module's error for an account no longer connected (its UnknownAccountError):
// the same code and status, an error the core sees only through them.
type unknownMailAccount string

func (u unknownMailAccount) Error() string {
	return i18n.T(i18n.En, "mail-errors.account_unknown", i18n.Args{"account": string(u)})
}
func (unknownMailAccount) Code() string    { return "mail_account_unknown" }
func (unknownMailAccount) HTTPStatus() int { return http.StatusNotFound }
