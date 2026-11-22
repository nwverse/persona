package proto

// Clipboard sharing between the phone and a computer (#3064, docs/operations/clipboard.md): the node reads and writes
// the clipboard of the session it runs in (clipboard.get / clipboard.set), the manager keeps a per-machine switch
// (off by default) and serves GET / PUT /api/v1/machines/{mid}/clipboard.

// Clipboard kinds (ClipboardContent.Kind).
const (
	ClipboardKindText  = "text"
	ClipboardKindImage = "image"
	ClipboardKindEmpty = "empty"
	// ClipboardKindConcealed: what is on the clipboard is marked secret or transient by the app that put it there (a
	// password manager: org.nspasteboard.ConcealedType / TransientType on macOS, ExcludeClipboardContentFromMonitorProcessing
	// on Windows, x-kde-passwordManagerHint on Linux). Reported without its content.
	ClipboardKindConcealed = "concealed"
)

// Size limits. A request body to the manager is at most ClipboardBodyMost; a decoded image at most ClipboardImageMost
// (base64 grows it by a third, so it still fits the body), text at most ClipboardTextMost bytes of UTF-8.
const (
	ClipboardBodyMost  = 10 << 20
	ClipboardImageMost = 7 << 20
	ClipboardTextMost  = 4 << 20
)

// Error codes of clipboard.get / clipboard.set (node → manager) and of the clipboard routes (manager → apps).
const (
	// CodeClipboardUnsupported: this computer has no clipboard the node can reach (its message is a reason code).
	CodeClipboardUnsupported = "clipboard_unsupported"
	// CodeClipboardDisabled: the user has not turned clipboard sharing on for this computer.
	CodeClipboardDisabled = "clipboard_disabled"
	// CodeClipboardFailed: the clipboard was there but reading or writing it failed.
	CodeClipboardFailed = "clipboard_failed"
	// CodeClipboardBadImage: an image that is not a PNG or JPEG.
	CodeClipboardBadImage = "clipboard_bad_image"
)

// Reason codes for an unsupported clipboard (ClipboardSupport.Reason, Machine.Clipboard.ReasonCode). The manager
// turns them into the user's words (devices-clipboard.reason_<code>).
const (
	ClipboardReasonNoDisplay    = "no_display"    // Linux without DISPLAY / WAYLAND_DISPLAY: no graphical session
	ClipboardReasonNoTool       = "no_tool"       // Linux with a display but neither wl-clipboard nor xclip
	ClipboardReasonNoSession    = "no_session"    // Windows session 0 (a service): no interactive desktop
	ClipboardReasonOS           = "os"            // an OS the node has no clipboard code for
	ClipboardReasonNodeOutdated = "node_outdated" // the node predates clipboard.get / clipboard.set
)

// ClipboardImage is an image on the clipboard. Data is the file's bytes (base64 in JSON); the node always reads PNG.
type ClipboardImage struct {
	Mime string `json:"mime"`
	Data []byte `json:"data"`
}

// ClipboardContent is clipboard.get's answer and clipboard.set's params (Kind is ignored there: Text or Image says).
type ClipboardContent struct {
	Kind  string          `json:"kind"`
	Text  *string         `json:"text,omitempty"`
	Image *ClipboardImage `json:"image,omitempty"`
}

// ClipboardSupport is node.info's clipboard: whether this node can reach a clipboard here, and why not (a reason code).
type ClipboardSupport struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

// MachineClipboard is proto.Machine's clipboard: whether the computer can share its clipboard, whether the user turned
// it on for this computer (off by default), and, when it cannot, why (Reason in the user's words, ReasonCode stable).
type MachineClipboard struct {
	Supported  bool   `json:"supported"`
	Enabled    bool   `json:"enabled"`
	Reason     string `json:"reason,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}
