package i18n

import "github.com/nwverse/persona/server/internal/proto"

// ClipboardStatus is a copy of c with Reason in l's words for its ReasonCode (devices-clipboard.reason_<code>); an
// unknown code keeps the Reason it had. The manager reports only the code: the reader's language is the hub's to know.
func ClipboardStatus(l Lang, c *proto.MachineClipboard) *proto.MachineClipboard {
	if c == nil {
		return nil
	}
	out := *c
	if c.ReasonCode != "" {
		if key := "devices-clipboard.reason_" + c.ReasonCode; T(l, key) != key {
			out.Reason = T(l, key)
		}
	}
	return &out
}
