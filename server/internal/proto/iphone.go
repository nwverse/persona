package proto

import (
	"encoding/json"
	"time"
)

// IPhone is what a computer has of the iPhones it reaches over RemotePairing through the device bridge, which the
// assistant may operate (the built-in extension iphone, docs/extensions/iphone.md). In proto.Machine only while the
// extension is on. Enabled is the manager's: the user turned it on for this computer; the rest is what the node found
// when last asked (the iphone program's status).
type IPhone struct {
	Enabled bool `json:"enabled"`
	// Installed: the device bridge (persona-iphone-bridge) is on this computer; Version, its version.
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// Devices: the paired iPhones, and those plugged in over USB that can be paired.
	Devices []IPhoneDevice `json:"devices"`
	// Note: what is missing on this computer as a whole (no bridge, nothing paired, an old node), in Chinese for the
	// hub to translate.
	Note    string    `json:"note,omitempty"`
	Checked time.Time `json:"checked,omitempty"`
	// Job: pairing or forgetting an iPhone from the app (kind pair | forget), the one running or the last one since the
	// node started.
	Job *ComputerJob `json:"job,omitempty"`
}

// IPhoneDevice is one iPhone as the node sees it. ID is the node's stable id for it (from its pairing record, the same
// on every computer that paired it); ProductType ("iPhone16,2") and IOS ("18.1") come from the record and the device.
type IPhoneDevice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProductType string `json:"product_type,omitempty"`
	IOS         string `json:"ios,omitempty"`
	// Paired: this computer holds a pairing record for it; USB: it is plugged in now (one that is can be paired).
	Paired bool `json:"paired"`
	USB    bool `json:"usb,omitempty"`
	// State of the link to a paired one: idle | connecting | connected | backoff | failed; Transport: network | usb.
	State     string `json:"state,omitempty"`
	Transport string `json:"transport,omitempty"`
	// Connected: the link is up and the device answered just now; Locked: it said its screen is locked.
	Connected bool `json:"connected"`
	Locked    bool `json:"locked"`
	// Capabilities: which groups of operations work now (screenshot, ax_tree, input, apps), as the device said.
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	// Ready: it can be operated now. Note says what is missing and what to do, in Chinese for the hub to translate.
	Ready bool   `json:"ready"`
	Note  string `json:"note,omitempty"`
	// Allowed is the manager's: the computer is on and this iPhone consented.
	Allowed bool `json:"allowed"`
}

// IPhoneCall is one call of the iphone program: op status | look | do on a device ("" the only paired one), in the
// assistant's conversation session, with the tool's arguments. Approved is the approval id the user allowed (passed
// back as the node gave it). TimeoutMS bounds the node's answer (it clamps it to 1–120 s, 30 s when 0).
type IPhoneCall struct {
	Device    string          `json:"device,omitempty"`
	Session   string          `json:"session,omitempty"`
	Op        string          `json:"op"`
	Args      json.RawMessage `json:"args,omitempty"`
	Approved  string          `json:"approved,omitempty"`
	TimeoutMS int             `json:"timeout_ms,omitempty"`
}

// IPhoneResult is what an iphone call answered: its JSON (Output), or Text, and a screenshot; or that the step needs the
// user's approval first (ApprovalSummary says what for; the call is made again with Approved = ApprovalID once given).
type IPhoneResult struct {
	Output          json.RawMessage `json:"output,omitempty"`
	Text            string          `json:"text,omitempty"`
	Image           *Image          `json:"image,omitempty"`
	NeedsApproval   bool            `json:"needs_approval,omitempty"`
	ApprovalSummary string          `json:"approval_summary,omitempty"`
	ApprovalID      string          `json:"approval_id,omitempty"`
}

// A failed iphone call is an RPC error whose data says whether the operation may have reached the iPhone:
// IPhoneNotSent when it was refused before anything was handed to the device (safe to try again), IPhoneMaybeSent
// otherwise (never retried by itself).
const (
	IPhoneNotSent   = "no"
	IPhoneMaybeSent = "unknown"
)

// IPhoneErrorData is a failed iphone call's error data.
type IPhoneErrorData struct {
	Sent string `json:"sent"`
}
