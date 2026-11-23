//go:build !persona_without_lark && !persona_core_only

package all

import (
	"net/http"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/botport"
	"github.com/nwverse/persona/server/internal/modules/lark"
	"github.com/nwverse/persona/server/internal/modules/lark/feishubot"
)

// The Feishu / Lark module: the bot's client the extension host drives (internal/botport).
func init() {
	modules = append(modules, lark.Module{})
	botport.Provide(botport.Provider{
		New: func(appID, appSecret, domain string, h *http.Client) botport.Client {
			c := feishubot.New(appID, appSecret, domain)
			if h != nil {
				c = c.WithHTTP(h)
			}
			return c
		},
		NewConn: func(api botport.Client, handler botcard.Handler, logf func(string, ...any)) botport.Conn {
			return feishubot.NewConn(api.(*feishubot.Client), handler, logf)
		},
		NewRegistrar: func(host string) botport.Registrar {
			r := feishubot.NewRegistrar()
			if host != "" {
				r = r.WithHost(host)
			}
			return r
		},
	})
}
