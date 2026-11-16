package assistantwire

// MethodCallRing is call.ring {call} → CallRingResult: a manager has the hub ring the owner's phones for an urgent call
// (docs/assistant/urgent-call.md). The manager decided the call may ring; the hub only delivers it, as a VoIP push to
// every phone registered for calls, and answers once APNs or the relay took or refused each one.
const MethodCallRing = "call.ring"

// CallRing is one ring of an urgent call. The phone gets it sealed and names the call from it.
type CallRing struct {
	ID             string `json:"id"`              // the call's id: the phone asks the manager for the matter by it
	Title          string `json:"title"`           // what the lock screen shows, a few words
	ConversationID string `json:"conversation_id"` // the matter's conversation, where the voice call talks
	Attempt        int    `json:"attempt"`         // 1 the first ring, 2 the retry after a missed one
	// TTL is how many seconds APNs keeps trying to deliver it (0: the relay's default, 30); a phone that was offline
	// longer is not rung.
	TTL int `json:"ttl,omitempty"`
}

// CallRingRequest is call.ring's params.
type CallRingRequest struct {
	Call CallRing `json:"call"`
}

// CallRingResult is call.ring's answer: how many phones APNs (or the relay) took the ring for. With none, Reason says
// why: CallNoPhone (no phone registered for calls) or CallRefused (every push was refused or failed); the manager
// then sends the matter as a P0 push instead.
type CallRingResult struct {
	Devices int    `json:"devices"`
	Reason  string `json:"reason,omitempty"`
}

// Reasons a call.ring rang no phone.
const (
	CallNoPhone = "no_phone"
	CallRefused = "refused"
)
