package herdr

import (
	"context"
	"errors"
	"strings"
)

// Integration is one of herdr's built-in agent integrations as its server sees it (integration.list, herdr's JSON
// integration API, 0.9 and later): Target is its id (antigravity_cli), Command the program it is for (agy), Available
// whether the server finds that program, State current, outdated or not_installed. The server checks the files in its
// own environment, which is the environment its panes start with.
type Integration struct {
	Target    string `json:"target"`
	Label     string `json:"label"`
	Command   string `json:"command"`
	Available bool   `json:"available"`
	State     string `json:"state"`
}

// The states integration.list reports. An outdated integration is installed and still reports; reinstalling it
// updates it.
const (
	IntegrationCurrent      = "current"
	IntegrationOutdated     = "outdated"
	IntegrationNotInstalled = "not_installed"
)

// AntigravityIntegration is the target of herdr's Antigravity CLI integration: the hook that reports a pane's agy
// conversation (pane.report_agent_session, source herdr:antigravity_cli).
const AntigravityIntegration = "antigravity_cli"

// Integrations is integration.list of the server on c's socket. Only the socket has it (`herdr integration status`
// is text for people): a herdr from before the JSON integration API (0.8.x) answers UnknownMethod.
func (c *Client) Integrations(ctx context.Context) ([]Integration, error) {
	var res struct {
		Integrations []Integration `json:"integrations"`
	}
	if err := c.rpc(ctx, "integration.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Integrations, nil
}

// UnknownMethod: err is herdr's answer that it does not know the method it was asked (an older herdr).
func UnknownMethod(err error) bool {
	var he *Error
	return errors.As(err, &he) && he.Code == "invalid_request" && strings.Contains(he.Message, "unknown variant")
}
