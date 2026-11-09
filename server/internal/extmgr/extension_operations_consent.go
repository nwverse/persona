package extmgr

import (
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// opPreviewed is op with its preview made: it waits for the user's consent, or goes on when it was retried after
// the user agreed to just this (the same digests). go on installs in the background.
func (h *Manager) opPreviewed(op *extOp, pv *extension.Preview, goOn func(refs []bundleRef)) {
	auto := false
	var refs []bundleRef
	h.update(op, func(v *ExtensionOperation) bool {
		m := pv.Manifest
		v.Extension, v.Name, v.Version = m.ID, m.Name, m.Version
		if pv.Upgrade {
			v.Kind, v.From = opUpgrade, pv.From
		}
		if pv.Market != "" {
			v.Market, v.Source = pv.Market, extension.MarketOrigin(pv.Market, m.ID)
		}
		setStep(v, extension.StepCheck, extension.StepDone)
		auto = op.consented != "" && op.consented == pv.Digest && len(pv.Unresolved) == 0
		for _, b := range pv.Bundle {
			if op.consentedBundle[b.Manifest.ID] != b.Digest {
				auto = false
			}
			refs = append(refs, bundleRef{PreviewID: b.ID, Digest: b.Digest})
		}
		v.PreviewID = pv.ID
		setStep(v, extension.StepConsent, extension.StepRunning)
		if !auto {
			v.State = opNeedsConsent
		}
		return true
	})
	if auto && goOn != nil {
		go goOn(refs)
	}
}

// claimOpOfPreview takes the operation waiting for consent with preview previewID for one install, nil when none
// is. It is marked running at once, under the lock, so a second install of the same preview (a double click, the
// web and the CLI together) cannot go on with it: that one is an installing_already error while the claimed one runs, and
// never reaches the registry, so it can neither end the install nor take the preview from it. installing pushes the
// change.
func (h *Manager) claimOpOfPreview(previewID string) (*extOp, error) {
	if previewID == "" {
		return nil, nil
	}
	ps := h.extensions
	ps.ops.mu.Lock()
	defer ps.ops.mu.Unlock()
	for _, op := range ps.ops.byID {
		switch {
		case op.v.PreviewID == previewID && op.v.State == opNeedsConsent:
			op.v.State, op.v.PreviewID, op.v.UpdatedAt = opRunning, "", time.Now()
			op.claimed = previewID
			return op, nil
		case op.claimed == previewID && op.v.State == opRunning:
			return nil, i18n.Err("hub-extension_operations.installing_already")
		}
	}
	return nil, nil
}

// claimedByMarket says whether an install of a market's extension took over the operation that was waiting for consent
// with preview previewID (claimOpOfPreview): the preview was that market's, also once the install has used it up. It
// is known only while that operation is kept.
func (h *Manager) claimedByMarket(previewID string) bool {
	if previewID == "" {
		return false
	}
	ps := h.extensions
	ps.ops.mu.Lock()
	defer ps.ops.mu.Unlock()
	for _, op := range ps.ops.byID {
		if op.claimed == previewID && op.ofMarket {
			return true
		}
	}
	return false
}

// opForInstall is the operation an install of preview pv goes on with: the one that made the preview, or a new one
// whose first steps are done already (a preview made before operations, or by the admin UI). It fails while another
// install goes on with the preview's operation.
func (h *Manager) opForInstall(pv *extension.Preview, previewID, actor string, l i18n.Lang) (*extOp, error) {
	if op, err := h.claimOpOfPreview(previewID); op != nil || err != nil {
		return op, err
	}
	kind, id := opInstall, ""
	if pv != nil {
		id = pv.Manifest.ID
		if pv.Upgrade {
			kind = opUpgrade
		}
	}
	// A preview that a market's install used up was that market's: a phone may follow and clear this operation too.
	op := h.startOp(kind, id, actor, l, (pv != nil && pv.Market != "") || (pv == nil && h.claimedByMarket(previewID)))
	h.update(op, func(v *ExtensionOperation) bool {
		for _, s := range []string{extension.StepDownload, extension.StepVerify, extension.StepUnpack, extension.StepCheck} {
			setStep(v, s, extension.StepSkipped)
		}
		if pv != nil {
			v.Name, v.Version = pv.Manifest.Name, pv.Manifest.Version
			if pv.Market != "" {
				v.Market, v.Source = pv.Market, extension.MarketOrigin(pv.Market, pv.Manifest.ID)
			}
		}
		return true
	})
	return op, nil
}

// installing marks op as agreed to (digest and its bundle's) and installing.
func (h *Manager) installing(op *extOp, pv *extension.Preview, digest string) {
	h.update(op, func(v *ExtensionOperation) bool {
		op.consented = digest
		op.consentedBundle = map[string]string{}
		if pv != nil {
			if op.consented == "" {
				op.consented = pv.Digest // an upgrade asking for nothing more needs no digest
			}
			for _, b := range pv.Bundle {
				op.consentedBundle[b.Manifest.ID] = b.Digest
			}
		}
		v.State, v.PreviewID = opRunning, ""
		setStep(v, extension.StepConsent, extension.StepDone)
		setStep(v, extension.StepTools, extension.StepRunning) // the registry says done or skipped, then install runs
		return true
	})
}
