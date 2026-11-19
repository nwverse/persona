package assistant

import (
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What a to-do write that failed did with the owner's words (#10465, docs/protocol/protocol.md "To-do writes"): a
// message on the conversation route, an answer on the asks route or the accept route says it in its error's
// delivered, so the hub's todo_send_state and its receipt say failed only for a write that typed nothing, and a phone
// never sends again under a new op id what may be with the Agent already. Words reach an Agent only through
// sendToTaskAgent's session.send or startTaskSuccessor's session.start: an error of either may have typed them when
// sendUnknown says so (the rule agentSendNote.sent follows) or it reads as a timeout or a dropped link, and typed
// nothing otherwise; what fails after one of them took the words is wordsSent; any other error came before anything
// went out.

// codeSendInDoubt is the code of a to-do write whose words may be with its Agent already, or are: it is never sent
// again by itself, and its words say to look first.
const codeSendInDoubt = "send_in_doubt"

// wordsSent is a to-do write that failed after its words reached the Agent: the routed item, or the decision they
// answer, could not be recorded (the to-do may have been deleted meanwhile).
type wordsSent struct{ error }

func (e wordsSent) Unwrap() error { return e.error }

// writeDelivered is what err, a to-do write's failure, did with its words, as its answer's delivered says it: "yes"
// when they reached the Agent before what followed failed, "unknown" when the send or the start that carried them may
// have reached it, "none" when nothing was typed. Besides sendUnknown, any transport failure the API reads as a
// timeout or a dropped link is unknown (a deadline that ran out past the bus arrives as words only, apierr.NodeCode),
// unless it never went out, its computer was offline, or the asks could not be read or saved (errAcceptStore, which
// comes before any send, or instead of one).
func writeDelivered(err error) string {
	var sent wordsSent
	switch {
	case errors.As(err, &sent):
		return "yes"
	case errors.Is(err, rpc.ErrNotSent), errors.Is(err, apierr.ErrOffline), errors.Is(err, errAcceptStore):
		return "none"
	case sendUnknown(err), apierr.NodeCode(err) == "timeout", apierr.NodeCode(err) == "node_unreachable":
		return "unknown"
	}
	return "none"
}

// The words of a to-do write in doubt: a message's or an answer's, and whether it may have reached the Agent
// (delivered "unknown") or did, with what followed failed ("yes").
var (
	routeInDoubtKeys = map[string]string{
		"unknown": "assistant-tasks.route_in_doubt", "yes": "assistant-tasks.route_sent_not_kept"}
	answerInDoubtKeys = map[string]string{
		"unknown": "assistant-tasks.answer_in_doubt", "yes": "assistant-tasks.answer_sent_not_kept"}
)

// writeInDoubt writes err, a to-do write whose words may be with its Agent already (delivered "unknown") or are
// ("yes"), with status: send_in_doubt, never retryable, in words from keys that say so in the request's language,
// and what failed as the detail. Never a 2xx: the hub reads a 2xx as delivered.
func writeInDoubt(w http.ResponseWriter, status int, err error, delivered string, keys map[string]string) {
	l := i18n.OfWriter(w)
	body := apierr.LocalizedBody(status, codeSendInDoubt, i18n.ErrEnglish(keys[delivered]), l)
	body["delivered"], body["retryable"] = delivered, false
	body["detail"] = clip(oneLine(i18n.Localize(err, l)), 300)
	apierr.WriteBody(w, status, body)
}

// writeTypedNothing writes err, a refusal of a to-do write that typed nothing, with code ("" for the status's) in the
// request's language, and delivered "none": the hub's receipt of it is failed, and the phone may send it again under a
// new op id.
func writeTypedNothing(w http.ResponseWriter, status int, code string, err error) {
	body := apierr.LocalizedBody(status, code, err, i18n.OfWriter(w))
	body["delivered"] = "none"
	apierr.WriteBody(w, status, body)
}

// writeRouteError writes err, a message to a to-do the conversation route did not deliver: 409, as every refusal of
// this route has been (the apps installed show its words), with delivered; words that may be with the Agent already
// are send_in_doubt and say so, instead of the refusal's words.
func writeRouteError(w http.ResponseWriter, err error) {
	if delivered := writeDelivered(err); delivered != "none" {
		writeInDoubt(w, http.StatusConflict, err, delivered, routeInDoubtKeys)
		return
	}
	writeTypedNothing(w, http.StatusConflict, "", err)
}
