package hub

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
)

type trustedSubmissionKey struct{}

// captureHumanSubmission runs before to-do, project or model-context augmentation.
// A cached principal or a caller-supplied provenance descriptor is not evidence.
// The brief DB admission is released here, never held across session startup.
func (h *Hub) captureHumanSubmission(r *http.Request, text string) *http.Request {
	p, ok := r.Context().Value(principalKey{}).(principal)
	if !ok || p.ID == "" || p.Role != roleFull || h.store == nil || h.isMember() {
		return r
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return r
	}
	hash := hashToken(strings.TrimPrefix(auth, "Bearer "))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	token, err := h.store.DeviceTokenByHash(ctx, hash)
	if err != nil || token == nil || token.ID != p.ID || token.Role != roleFull || token.Kind == "link" {
		return r
	}
	caller := devicewire.Caller{Kind: "device", Issuer: h.hubID(), CredentialID: token.ID, AdmissionID: rand.Text()}
	if s := h.ownerAuthStore(); s != nil {
		if binding, release, err := s.AdmitOwner(ctx, h.hubID(), hash); err == nil {
			caller.OwnerSubject = *binding.Subject
			caller.BindingRevision = binding.Revision
			release()
		}
	}
	out := devicewire.WithCaller(r.Context(), caller)
	out = context.WithValue(out, trustedSubmissionKey{}, devicewire.Submission{ID: rand.Text(), Kind: "human_input", OriginalText: text})
	return r.WithContext(out)
}

// attachNodeCaller excludes manager-originated device.call, regardless of any
// principal retained on a tool's transport. Reserved fields live in the envelope.
func attachNodeCaller(ctx context.Context, p *devicewire.NodeCall) {
	caller := devicewire.Caller{Kind: "system"}
	if _, manager := callerOf(ctx); !manager {
		if c := devicewire.CallerOf(ctx); c.Kind == "device" {
			caller = c
		}
	}
	p.Caller = &caller
	if caller.Kind == "device" {
		if submission, ok := ctx.Value(trustedSubmissionKey{}).(devicewire.Submission); ok {
			p.Submission = &submission
		}
	}
}

// assistantFromMode keeps session.send's from_mode only on the assistant's own device.call: the permission-mode class of
// the words a message carries (a task worker's, in a supervision message to its leader) is the assistant's to state, from
// that worker's session, and a node states it to a Claude outside Persona's terminals (node claudebg.go, #9607). An app,
// a relay, a channel or another manager never states it: theirs is dropped.
func assistantFromMode(ctx context.Context, raw json.RawMessage) json.RawMessage {
	if id, ok := callerOf(ctx); ok && id == assistantwire.AssistantID {
		return raw
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	if _, ok := fields["from_mode"]; !ok {
		return raw
	}
	delete(fields, "from_mode")
	out, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}
