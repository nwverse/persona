package mail

import (
	"net/http"

	"github.com/nwverse/persona/server/internal/i18n"
)

// UnknownAccountError names an account that is no longer connected to this manager. It keeps the expected
// failure's code, status and localized summary when a caller exposes it through another API, such as translation.
type UnknownAccountError struct {
	Account string
}

func (e *UnknownAccountError) Error() string { return e.Text(i18n.En) }

// Text is the account-specific explanation in the client's language.
func (e *UnknownAccountError) Text(l i18n.Lang) string {
	return i18n.T(l, "mail-errors.account_unknown", i18n.Args{"account": e.Account})
}

func (*UnknownAccountError) Code() string    { return "mail_account_unknown" }
func (*UnknownAccountError) HTTPStatus() int { return http.StatusNotFound }
