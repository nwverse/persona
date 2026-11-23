//go:build !persona_without_mail && !persona_core_only

package all

import (
	"github.com/nwverse/persona/server/internal/extmgr/mailport"
	"github.com/nwverse/persona/server/internal/mail"
)

// Mail reaches the extension manager through its port, not the kernel: its routes and tools are the extension
// manager's (docs/architecture/core-and-modules.md "Mail").
func init() { mailport.Provide(mail.Open) }
