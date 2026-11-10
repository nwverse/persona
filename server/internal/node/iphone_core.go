package node

// The iphone program's driver on a real computer: the core of internal/iphonectl (element numbers, reading the screen,
// limits, whether an input took effect, the owner's approval), reaching the iPhone through the bridge's operations on
// the session the program hands it (docs/extensions/iphone-control.md).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/iphonectl"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// iphoneControl is iphoneDriver over one iphonectl.Controller for the whole node: the controller keeps each
// conversation's numbered read and the step waiting for the owner in memory, so every call must reach the same one.
type iphoneControl struct {
	once sync.Once
	c    *iphonectl.Controller
}

func (d *iphoneControl) controller() *iphonectl.Controller {
	d.once.Do(func() { d.c = iphonectl.NewController(iphonectl.Options{Rules: iphoneRules()}) })
	return d.c
}

// iphoneRules are the rules the core starts with. Persona's own apps come with the calls (proto.IPhoneCall.SelfApps):
// the hub knows them, the node has no config of its own that does.
func iphoneRules() iphonectl.Rules {
	return iphonectl.Rules{}
}

// Run is one look or do: the core answers within the call's context whatever the bridge does, so a failure of the
// iPhone or of a step is in the answer (what was sent, what was not). Only a call the core turned down as a whole
// before sending anything is an error, with the core's code for why (bad_args, not_pending, busy: the approval still
// pending), saying nothing was sent, in the core's words.
func (d *iphoneControl) Run(ctx context.Context, r iphoneRun) (iphoneOutcome, error) {
	w := &iphonectl.Wire{Call: iphoneWireCall(r.Bridge), TempDir: r.TempDir}
	resp := d.controller().Handle(ctx, w, iphonectl.Request{Device: r.Device, Session: r.Session, Op: r.Op, Args: r.Args,
		Approved: r.Approved, Confirm: r.Confirm, SelfApps: r.SelfApps, SelfNames: r.SelfNames})
	if resp.Refused != "" {
		return iphoneOutcome{}, iphoneErr(resp.Refused, strings.TrimSpace(resp.Text), proto.IPhoneNotSent)
	}
	out := iphoneOutcome{Output: resp.Output, Text: resp.Text, NeedsApproval: resp.NeedsApproval, ApprovalID: resp.ApprovalID}
	if resp.Approval != nil {
		// Data for the manager, which words it in the owner's language; the node never phrases it.
		a, err := iphoneApproval(resp.Approval)
		if err != nil {
			return iphoneOutcome{}, err
		}
		out.Approval = a
	}
	if img := resp.Image; img != nil {
		out.ImageFile = img.File
		if out.ImageFile == "" && len(img.Data) > 0 {
			out.ImageFile = iphoneSaveImage(r.TempDir, img.Data)
		}
	}
	return out, nil
}

// iphoneApproval is the core's approval as the manager reads it: proto.IPhoneApproval mirrors iphonectl.Approval
// field by field under the same JSON names (TestIPhoneApprovalMirrorsTheCore keeps the two in step).
func iphoneApproval(a *iphonectl.Approval) (*proto.IPhoneApproval, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	var out proto.IPhoneApproval
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// iphoneSaysMaybeSent: err's data says the operation may have reached the iPhone (the session handed it over and lost
// the answer), whatever its code.
func iphoneSaysMaybeSent(err error) bool {
	var coded *rpc.Error
	if !errors.As(err, &coded) || len(coded.Data) == 0 {
		return false
	}
	var d proto.IPhoneErrorData
	return json.Unmarshal(coded.Data, &d) == nil && d.Sent == proto.IPhoneMaybeSent
}

// iphoneSaveImage writes a screenshot the bridge answered inline into dir, where the program reads it from; "" when
// it can't.
func iphoneSaveImage(dir string, data []byte) string {
	f, err := os.CreateTemp(dir, "screenshot-*")
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return ""
	}
	return f.Name()
}

// iphoneWireCall is the session's call as the core's wire: an error with a code becomes the core's *Error with the
// same code, marked NotSent when the session or the code says the operation never reached the iPhone (iphoneSent)
// and MaybeSent when the session says it may have, so the core never counts such an input as refused; any other error
// (the context's) passes as it is, so the core sees its own deadline.
func iphoneWireCall(bridge func(context.Context, string, json.RawMessage) (json.RawMessage, error)) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
		out, err := bridge(ctx, op, args)
		if err == nil {
			return out, nil
		}
		code := iphoneErrorCode(err)
		if code == "" {
			return nil, err
		}
		msg := err.Error()
		var coded *rpc.Error
		if errors.As(err, &coded) {
			msg = coded.Message
		}
		return nil, &iphonectl.Error{Code: code, Message: msg, NotSent: iphoneSent(err) == proto.IPhoneNotSent,
			MaybeSent: iphoneSaysMaybeSent(err)}
	}
}
