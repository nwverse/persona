package prompts

import "strings"

// PeerMessage is a message the user sends from the phone to a Claude session running outside Persona's terminals,
// through that session's peer inbox (node/claudepeer.go): Claude Code shows it to the model as one from "another
// Claude session", so it says whose it is and that the answer belongs in the session (agents/peer_message).
type PeerMessage struct {
	Text string // what the user wrote
}

func RenderPeerMessage(m PeerMessage) string { return render("agents/peer_message", m) }

// peerMessageHead is what agents/peer_message puts before the user's words.
var peerMessageHead, _, _ = strings.Cut(render("agents/peer_message", PeerMessage{Text: "\x00"}), "\x00")

// PeerMessageBody is the user's words in a rendered peer message (what the session's transcript records as the
// message's body); ok false when s is not one.
func PeerMessageBody(s string) (body string, ok bool) {
	return strings.CutPrefix(s, peerMessageHead)
}
