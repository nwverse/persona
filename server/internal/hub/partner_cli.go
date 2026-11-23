package hub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/clitext"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Partner hubs from the command line on the hub's machine (docs/server/partner-hubs.md): the apps' routes under
// /api/v1/admin/cli/partners, over loopback with the master token (peerCLIRoutes, partnerCLIRoutes).

// partnerUsage is `hub partner`'s usage line.
const partnerUsage = "usage: personad hub partner code | add <code>|- | list | dial <hub_id> auto|never | remove <hub_id>"

// PartnerCommand is `personad hub partner code`, `add <code>|-`, `list`, `dial <hub_id> auto|never` and
// `remove <hub_id>`. ctx is the command's (Ctrl-C ends it); an argument mistake is a clitext.UsageError, found before
// hub.json is read. Help asked for prints the usage on out (exit 0).
func PartnerCommand(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if peerHelpAsked(args) {
		fmt.Fprintln(out, clitext.Named(partnerUsage))
		return nil
	}
	if err := checkPartnerArgs(args); err != nil {
		return err
	}
	cfg, err := runningHubConfig()
	if err != nil {
		return err
	}
	l := i18n.Terminal()
	switch args[0] {
	case "code":
		var code partnerCode
		if err := cfg.adminCall(ctx, http.MethodPost, "/api/v1/admin/cli/partners/code", nil, &code); err != nil {
			return err
		}
		fmt.Fprintln(out, code.Code)
		fmt.Fprintln(os.Stderr, "\n"+i18n.T(l, "hub-partner_cli.code_hint", i18n.Args{
			"count":   max(1, int(time.Until(code.ExpiresAt).Round(time.Minute).Minutes())),
			"command": clitext.Named("personad hub partner add"),
		}))
		return nil
	case "add":
		code := args[1]
		if code == "-" {
			if code, err = readPeerCode(ctx, l, stdin); err != nil {
				return err
			}
		}
		var v partnerHubView
		if err := cfg.adminCall(ctx, http.MethodPost, "/api/v1/admin/cli/partners", map[string]string{"code": code}, &v); err != nil {
			return err
		}
		fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.added", i18n.Args{"name": clitext.Inert(v.Name), "id": clitext.Inert(v.HubID),
			"state": partnerStateText(l, v)}))
		return nil
	case "list":
		var res struct {
			Enabled bool             `json:"enabled"`
			Member  bool             `json:"member"`
			Hubs    []partnerHubView `json:"hubs"`
		}
		if err := cfg.adminCall(ctx, http.MethodGet, "/api/v1/admin/cli/partners", nil, &res); err != nil {
			return err
		}
		switch {
		case res.Member:
			fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.member"))
		case !res.Enabled:
			fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.off"))
		}
		if len(res.Hubs) == 0 {
			fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.empty"))
		}
		// Names and ids are the other hub's text: inert, one line per hub.
		for _, v := range res.Hubs {
			fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.line", i18n.Args{"id": clitext.Inert(v.HubID), "name": clitext.Inert(v.Name),
				"dial": partnerDialText(l, v.Dial), "state": partnerStateText(l, v)}))
		}
		return nil
	case "dial":
		var v partnerHubView
		if err := cfg.adminCall(ctx, http.MethodPatch, "/api/v1/admin/cli/partners/"+url.PathEscape(args[1]), map[string]string{"dial": args[2]}, &v); err != nil {
			return err
		}
		fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.dial_set", i18n.Args{"name": clitext.Inert(v.Name), "dial": partnerDialText(l, v.Dial)}))
		return nil
	case "remove":
		if err := cfg.adminCall(ctx, http.MethodDelete, "/api/v1/admin/cli/partners/"+url.PathEscape(args[1]), nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, i18n.T(l, "hub-partner_cli.removed"))
		return nil
	}
	return fmt.Errorf("unknown partner command %q", args[0]) // checkPartnerArgs let through a command this switch lacks
}

// checkPartnerArgs refuses what PartnerCommand would otherwise drop or misread: an extra word, a flag taken for an id
// or a code, a dial setting that is neither auto nor never.
func checkPartnerArgs(args []string) error {
	if len(args) == 0 {
		return clitext.UsageError(clitext.Named(partnerUsage))
	}
	rest := args[1:]
	id := func(s string) bool { return s != "" && !strings.HasPrefix(s, "-") }
	switch args[0] {
	case "code", "list":
		if len(rest) != 0 {
			return clitext.UsageError(clitext.Named(fmt.Sprintf("usage: personad hub partner %s (takes no arguments)", args[0])))
		}
	case "add":
		if len(rest) != 1 || (rest[0] != "-" && strings.HasPrefix(rest[0], "-")) {
			return clitext.UsageError(clitext.Named("usage: personad hub partner add <code> (- reads the code from stdin)"))
		}
	case "dial":
		if len(rest) != 2 || !id(rest[0]) || !slices.Contains([]string{"auto", "never"}, rest[1]) {
			return clitext.UsageError(clitext.Named("usage: personad hub partner dial <hub_id> auto|never"))
		}
	case "remove":
		if len(rest) != 1 || !id(rest[0]) {
			return clitext.UsageError(clitext.Named("usage: personad hub partner remove <hub_id>"))
		}
	default:
		return clitext.UsageError(clitext.Named(fmt.Sprintf("unknown partner command %q; %s", args[0], partnerUsage)))
	}
	return nil
}

// partnerStates are a partner's states as the command line words them.
var partnerStates = map[string]string{"connected": "hub-partner_cli.state_connected", "connecting": "hub-partner_cli.state_connecting",
	"offline": "hub-partner_cli.state_offline", "refused": "hub-partner_cli.state_refused"}

func partnerStateText(l i18n.Lang, v partnerHubView) string {
	s := clitext.Inert(v.State)
	if key := partnerStates[v.State]; key != "" {
		s = i18n.T(l, key)
	}
	if v.RTTMS != nil {
		s += i18n.T(l, "hub-partner_cli.state_rtt", i18n.Args{"ms": *v.RTTMS})
	}
	if v.Error != "" {
		s += i18n.T(l, "hub-partner_cli.state_error", i18n.Args{"error": clitext.Inert(v.Error)})
	}
	return s
}

func partnerDialText(l i18n.Lang, dial string) string {
	if dial == "never" {
		return i18n.T(l, "hub-partner_cli.dial_never")
	}
	return i18n.T(l, "hub-partner_cli.dial_auto")
}
