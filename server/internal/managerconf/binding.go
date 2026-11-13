package managerconf

import (
	"encoding/json"
	"errors"
	"net"
)

// Binding is the built-in manager's binding to its hub when it is a service of its own (docs/assistant/managers.md
// "The built-in manager as its own service"), in its manager.json under the keys a manager made in the app keeps
// them under: where the hub is, the token the hub gave it for a one-time code (`persona-manager join --builtin`), and
// the hub's TLS fingerprint on an https address with the hub's own certificate.
type Binding struct {
	Hub         string `json:"hub,omitempty"`
	HubID       string `json:"hub_id,omitempty"`
	Token       string `json:"token,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// bindingKeys are Binding's keys in manager.json.
var bindingKeys = []string{"hub", "hub_id", "token", "fingerprint"}

// ErrNotBound: the built-in manager's manager.json has no token (it was never bound, or the binding was dropped).
var ErrNotBound = errors.New("the built-in manager is not bound to a hub: persona-hub manager-code on the hub's " +
	"machine prints a code, persona-manager join --builtin <hub address> <code> binds it")

// ReadBinding is the binding in the built-in manager's manager.json in the data directory home; ErrNotBound without a
// token.
func ReadBinding(home string) (Binding, error) {
	var b Binding
	raw, err := ReadFile(DefaultFile(home))
	if err != nil {
		return b, err
	}
	if raw == nil {
		return b, ErrNotBound
	}
	o, err := ParseObject(raw)
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(o.Marshal(), &b); err != nil {
		return b, err
	}
	if b.Token == "" || b.Hub == "" {
		return b, ErrNotBound
	}
	return b, nil
}

// WriteBinding puts b into the built-in manager's manager.json in home (its other keys as they were; 0600, Edit), in
// place of any binding it had.
func WriteBinding(home string, b Binding) error {
	return Edit(DefaultFile(home), func(o *Object) error {
		for _, k := range bindingKeys {
			o.Del(k)
		}
		for k, v := range map[string]string{"hub": b.Hub, "hub_id": b.HubID, "token": b.Token, "fingerprint": b.Fingerprint} {
			if v != "" {
				if err := o.Set(k, v); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// The built-in manager's own service, as `persona-manager install --builtin` installs it and `persona-hub setup`
// names it: the systemd user unit BuiltinService+".service" on Linux, the launchd agent BuiltinLabel on a Mac.
const (
	BuiltinService = "persona-manager-assistant"
	BuiltinLabel   = "com.persona.manager.assistant"
)

// BuiltinCodeEnv is where `persona-manager install --builtin --join` (and `join --builtin` given no code) reads the
// one-time code from (`persona-hub setup` puts it there): not the command line, which any user of the machine can read
// while it runs, and the hub trades the built-in manager's code from its own machine without any other credential.
const BuiltinCodeEnv = "PERSONA_MANAGER_CODE"

// BuiltinInstallArgs is the command line `persona-hub setup` runs the manager's program with (personad's form): bind
// the built-in manager at hub with the code in BuiltinCodeEnv unless its binding works, then install its service.
func BuiltinInstallArgs(hub string) []string {
	return []string{"manager", "install", "--builtin", "--join", hub}
}

// LocalHubURL is the hub on this machine, from its listen address (hub.json's listen): loopback when it listens on
// every address, else the address it listens on; the default port on loopback when listen is not host:port.
func LocalHubURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://127.0.0.1:18660"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// BuiltinCheckArgs is the command line that says, before anything is changed, whether the manager's program can
// install the built-in manager's service here (exit 0): `persona-hub setup --manager-service` runs it first.
func BuiltinCheckArgs() []string {
	return []string{"manager", "install", "--builtin", "--check"}
}
