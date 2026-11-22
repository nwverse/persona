package devices

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"encoding/hex"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Sharing handlers consume only caller context installed by the authenticated
// bus transport. The public path is deliberately independent of role headers.
func (d *Devices) shareRoutes(mux *http.ServeMux) {
	wrap := func(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			c := devicewire.CallerOf(r.Context())
			public := strings.HasPrefix(r.URL.Path, "/api/public/agent-shares/")
			admin := strings.HasPrefix(r.URL.Path, "/api/v1/admin/sessions/")
			if (public && c.Kind != "anonymous") || (admin && (c.Kind != "admin" || c.Issuer == "" || c.AdmissionID == "")) || (!public && !admin && (c.Kind != "device" || c.Issuer == "" || c.OwnerSubject == "" || c.BindingRevision < 1 || c.AdmissionID == "")) {
				shareError(w, shareFault{403, "owner_enrollment_required"})
				return
			}
			if d.shareDB() == nil {
				shareError(w, shareFail("authority_unavailable"))
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			defer cancel()
			if err := fn(w, r.WithContext(ctx)); err != nil {
				shareError(w, err)
			}
		}
	}
	mux.HandleFunc("GET /api/v1/sessions/{mid}/{sid}/share-catalog", wrap(d.shareCatalog))
	mux.HandleFunc("POST /api/v1/sessions/{mid}/{sid}/share-preview", wrap(d.sharePreviewCreate))
	mux.HandleFunc("GET /api/v1/sessions/{mid}/{sid}/share-preview/{preview_id}", wrap(d.sharePreviewRead))
	mux.HandleFunc("POST /api/v1/agent-shares", wrap(d.shareCreate))
	mux.HandleFunc("GET /api/v1/agent-shares", wrap(d.shareList))
	mux.HandleFunc("GET /api/v1/agent-shares/{share_id}", wrap(d.shareGet))
	mux.HandleFunc("GET /api/v1/agent-shares/{share_id}/messages", wrap(d.shareMessages))
	mux.HandleFunc("PATCH /api/v1/agent-shares/{share_id}", wrap(d.sharePatch))
	mux.HandleFunc("DELETE /api/v1/agent-shares/{share_id}", wrap(d.shareRevoke))
	mux.HandleFunc("GET /api/public/agent-shares/{token}", wrap(d.shareReadPublic))
	mux.HandleFunc("GET /api/v1/admin/sessions/{mid}/{sid}/ownership-inspection", wrap(d.shareInspect))
	mux.HandleFunc("POST /api/v1/admin/sessions/{mid}/{sid}/owner", wrap(d.shareAttribute))
}
func shareEncode(w http.ResponseWriter, v any) error { return json.NewEncoder(w).Encode(v) }
func (d *Devices) shareCatalog(w http.ResponseWriter, r *http.Request) error {
	actor := shareActor(devicewire.CallerOf(r.Context()))
	id := r.URL.Query().Get("snapshot_id")
	var snap shareSnapshot
	if id == "" {
		if r.URL.Query().Get("cursor") != "" {
			return shareFault{400, "invalid_request"}
		}
		var err error
		snap, err = d.newSnapshot(r.Context(), r.PathValue("mid"), r.PathValue("sid"))
		if err != nil {
			var f shareFault
			reason := "authority_unavailable"
			if errors.As(err, &f) {
				reason = f.code
			}
			if reason == "not_found" {
				reason = "not_owner"
			}
			return shareEncode(w, map[string]any{"can_share": false, "reason": reason, "can_include_future": false, "messages": []any{}})
		}
		id = rand.Text()
	}
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		if snap.Version == "" {
			if err := t.Object(id, "snapshot", actor, &snap); err != nil {
				return shareFail("snapshot_changed")
			}
		} else if err := t.PutObject(id, "snapshot", actor, snap.Expires, snap); err != nil {
			return err
		}
		if snap.Machine != r.PathValue("mid") || snap.Session != r.PathValue("sid") || !ownerMatches(devicewire.CallerOf(r.Context()), snap.Owner) {
			return shareMissing()
		}
		if _, err := d.requireAuthority(r.Context(), snap.Owner); err != nil {
			return err
		}
		page, next, err := sharePage(t, r, actor, id, snap.Version, sharePublic(snap.Projection.Messages, false, id))
		if err != nil {
			return err
		}
		// Catalog selectors use the sealed manager IDs, not the public projector IDs.
		byPublic := map[string]string{}
		for _, m := range snap.Projection.Messages {
			byPublic[shareHash([]byte(id + "/" + m.Message.ID))[:32]] = m.Message.ID
		}
		items := []map[string]any{}
		for _, m := range page {
			items = append(items, map[string]any{"message_id": byPublic[m.ID], "role": m.Role, "text": m.Text, "selectable": true})
		}
		body = map[string]any{"can_share": true, "reason": nil, "can_include_future": true, "snapshot_id": id, "snapshot_version": snap.Version, "expires_at": snap.Expires, "complete": true, "total_messages": len(snap.Projection.Messages), "omitted_messages": snap.Projection.Omitted, "omissions": snap.Projection.Omissions, "messages": items, "next_cursor": next}
		shareReport(w, snap.Authority)
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}
func previewResponse(t *db.ShareTx, w http.ResponseWriter, r *http.Request, actor, id string, v sharePreview) (any, error) {
	page, next, err := sharePage(t, r, actor, id, v.Version, sharePublic(v.Messages, *v.Policy.Tools, id))
	if err != nil {
		return nil, err
	}
	var warning any
	if v.Insecure {
		warning = i18n.T(i18n.FromRequest(r).Or(i18n.En), "sharing.insecure_transport_warning")
	}
	return map[string]any{"transport_warning": warning, "preview_id": id, "expires_at": v.Expires, "policy_version": sharePolicyVersion, "selected_count": len(v.Messages), "omitted_count": v.Snapshot.Projection.Omitted, "messages": page, "next_cursor": next, "requires_insecure_transport_ack": v.Insecure}, nil
}
func (d *Devices) sharePreviewCreate(w http.ResponseWriter, r *http.Request) error {
	var p sharePolicy
	if err := shareJSON(w, r, &p); err != nil {
		return err
	}
	id := rand.Text()
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		v, err := d.preparePreview(r.Context(), t, p, r.PathValue("mid"), r.PathValue("sid"))
		if err != nil {
			return err
		}
		if err = t.PutObject(id, "preview", shareActor(devicewire.CallerOf(r.Context())), v.Expires, v); err != nil {
			return err
		}
		body, err = previewResponse(t, w, r, shareActor(devicewire.CallerOf(r.Context())), id, v)
		shareReport(w, v.Snapshot.Authority)
		return err
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}
func (d *Devices) sharePreviewRead(w http.ResponseWriter, r *http.Request) error {
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		actor := shareActor(devicewire.CallerOf(r.Context()))
		id := r.PathValue("preview_id")
		var v sharePreview
		if err := t.Object(id, "preview", actor, &v); err != nil {
			return shareFail("preview_changed")
		}
		if v.Snapshot.Machine != r.PathValue("mid") || v.Snapshot.Session != r.PathValue("sid") {
			return shareMissing()
		}
		if _, err := d.requireAuthority(r.Context(), v.Snapshot.Owner); err != nil {
			return err
		}
		var err error
		body, err = previewResponse(t, w, r, actor, id, v)
		shareReport(w, v.Snapshot.Authority)
		return err
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}

type shareCreateRequest struct {
	RequestID       string                    `json:"client_request_id"`
	Machine         string                    `json:"machine"`
	Session         string                    `json:"session_id"`
	SnapshotID      string                    `json:"snapshot_id"`
	SnapshotVersion string                    `json:"snapshot_version"`
	Selection       devicewire.ShareSelection `json:"selection"`
	Future          bool                      `json:"include_future"`
	Tools           bool                      `json:"show_tools"`
	Days            int                       `json:"expires_in_days"`
	Ack             shareAck                  `json:"acknowledgement"`
}

func shareRequestUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}

func (d *Devices) shareCreate(w http.ResponseWriter, r *http.Request) error {
	var req shareCreateRequest
	if err := shareJSON(w, r, &req); err != nil {
		return err
	}
	if !shareRequestUUID(req.RequestID) {
		return shareFault{400, "invalid_request"}
	}
	actor := shareActor(devicewire.CallerOf(r.Context()))
	digest := shareValueHash(req)
	// Capture the future frontier before taking the share writer fence.
	var futureOwner db.SessionShareOwnership
	var future devicewire.ShareProjection
	if req.Future {
		// Committed requests recover tokenless metadata even while the node is
		// offline; validate the preview before spending a fresh projection RPC.
		var replay *sealedShare
		if err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
			var err error
			replay, err = shareCreationReplay(r.Context(), t, actor, req.RequestID, digest)
			if err != nil || replay != nil {
				return err
			}
			p, err := targetPolicy(sharePolicy{Operation: "create", SnapshotID: req.SnapshotID, SnapshotVersion: req.SnapshotVersion, Selection: &req.Selection, Future: &req.Future, Tools: &req.Tools, Days: req.Days}, nil)
			if err != nil {
				return err
			}
			v, err := d.acknowledged(r.Context(), t, req.Ack, p, req.Machine, req.Session)
			if err != nil {
				return err
			}
			futureOwner, err = ownedShareTransaction(r.Context(), t, req.Machine, req.Session)
			if err == nil && futureOwner != v.Snapshot.Owner {
				return shareFail("snapshot_changed")
			}
			return err
		}); err != nil {
			return err
		}
		if replay != nil {
			shareReport(w, replay.Authority)
			w.WriteHeader(http.StatusConflict)
			return shareEncode(w, map[string]any{"code": "creation_already_committed", "share": replay.Meta})
		}
		var err error
		if future, err = d.projectShare(r.Context(), futureOwner, nil); err != nil {
			return err
		}
	}
	var body any
	status := 201
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		if replay, err := shareCreationReplay(r.Context(), t, actor, req.RequestID, digest); err != nil {
			return err
		} else if replay != nil {
			status = 409
			body = map[string]any{"code": "creation_already_committed", "share": replay.Meta}
			shareReport(w, replay.Authority)
			return nil
		}
		p, err := targetPolicy(sharePolicy{Operation: "create", SnapshotID: req.SnapshotID, SnapshotVersion: req.SnapshotVersion, Selection: &req.Selection, Future: &req.Future, Tools: &req.Tools, Days: req.Days}, nil)
		if err != nil {
			return err
		}
		v, err := d.acknowledged(r.Context(), t, req.Ack, p, req.Machine, req.Session)
		if err != nil {
			return err
		}
		owner, err := ownedShareTransaction(r.Context(), t, req.Machine, req.Session)
		if err != nil {
			return err
		}
		if owner != v.Snapshot.Owner {
			return shareFail("snapshot_changed")
		}
		retained, active, err := t.ShareCounts(owner.Issuer, owner.Subject, time.Now())
		if err != nil {
			return err
		}
		if active >= 20 || retained >= 1000 {
			return shareFail("share_limit")
		}
		origin, handle := d.sharingOrigin()
		if origin == "" || len(handle) != 16 {
			return shareFail("public_origin_unavailable")
		}
		now := time.Now().UTC()
		s := sealedShare{Owner: owner, Authority: v.Snapshot.Authority, Selection: req.Selection, Messages: append([]devicewire.ShareProjectionMessage(nil), v.Messages...), Frontier: v.Snapshot.Projection.Frontier, Excluded: map[string]bool{}}
		s.Meta = shareMetadata{ID: rand.Text(), Revision: 1, ContentVersion: 1, Future: req.Future, Tools: req.Tools, Created: now, Updated: now, Expires: now.Add(time.Duration(p.Days) * 24 * time.Hour), Status: "active", Publication: "frozen"}
		s.Meta.Selection.Mode = req.Selection.Mode
		s.Meta.Selection.Count = len(s.Messages)
		if req.Future {
			if futureOwner != owner {
				return shareFail("snapshot_changed")
			}
			applyShareFuture(&s, future)
		}
		raw := make([]byte, 48)
		copy(raw, handle)
		if _, err := rand.Read(raw[16:]); err != nil {
			return err
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		for i := range s.Messages {
			s.Messages[i].Message.ID = rand.Text()
		}
		row := db.ShareRow{ID: s.Meta.ID, Issuer: owner.Issuer, Subject: owner.Subject, Machine: req.Machine, Session: req.Session, Hash: shareHash([]byte(token))}
		if err = saveSealed(t, row, s); err != nil {
			return err
		}
		if err = t.PutCreation(actor, req.RequestID, digest, s.Meta.ID); err != nil {
			return err
		}
		body = map[string]any{"share": s.Meta, "public_url": origin + "/share/" + token}
		shareReport(w, s.Authority)
		return nil
	})
	if err != nil {
		return err
	}
	w.WriteHeader(status)
	return shareEncode(w, body)
}
func (d *Devices) manageShare(ctx context.Context, t *db.ShareTx, id string) (db.ShareRow, sealedShare, error) {
	row, s, err := loadSealed(t, id)
	if err != nil {
		return row, s, err
	}
	if !ownerMatches(devicewire.CallerOf(ctx), s.Owner) {
		return row, s, shareMissing()
	}
	o, err := ownedShareTransaction(ctx, t, s.Owner.Machine, s.Owner.SessionID)
	if err != nil || o != s.Owner {
		return row, s, shareMissing()
	}
	return row, s, nil
}
func (d *Devices) shareList(w http.ResponseWriter, r *http.Request) error {
	c := devicewire.CallerOf(r.Context())
	items := []shareMetadata{}
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		rows, err := t.ShareHeaders(c.Issuer, c.OwnerSubject)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if m := r.URL.Query().Get("machine"); m != "" && row.Machine != m {
				continue
			}
			if sid := r.URL.Query().Get("session_id"); sid != "" && row.Session != sid {
				continue
			}
			var s sealedShareHeader
			if err := json.Unmarshal(row.Header, &s); err != nil {
				return err
			}
			if s.Meta.ID != row.ID || !ownerMatches(c, s.Owner) {
				continue
			}
			o, err := t.SessionShareOwner(s.Owner.Machine, s.Owner.SessionID)
			if err != nil {
				return err
			}
			if o == nil || *o != s.Owner {
				continue
			}
			if s.Meta.Status == "active" && !s.Meta.Expires.After(time.Now()) {
				s.Meta.Status = "expired"
				s.Meta.Publication = "frozen"
			}
			items = append(items, s.Meta)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, map[string]any{"shares": items})
}
func (d *Devices) shareGet(w http.ResponseWriter, r *http.Request) error {
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		_, s, err := d.manageShare(r.Context(), t, r.PathValue("share_id"))
		if err != nil {
			return err
		}
		_, err = d.requireAuthority(r.Context(), s.Owner)
		var reason any
		if err != nil {
			reason = "authority_unavailable"
		}
		body = map[string]any{"share": s.Meta, "can_widen": err == nil && s.Meta.Status == "active", "reason": reason}
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}
func shareVersion(s sealedShare) string {
	return strconv.FormatInt(s.Meta.Revision, 10) + "/" + strconv.FormatInt(s.Meta.ContentVersion, 10)
}
func (d *Devices) shareMessages(w http.ResponseWriter, r *http.Request) error {
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		_, s, err := d.manageShare(r.Context(), t, r.PathValue("share_id"))
		if err != nil {
			return err
		}
		page, next, err := sharePage(t, r, shareActor(devicewire.CallerOf(r.Context())), s.Meta.ID, shareVersion(s), sharePublic(s.Messages, s.Meta.Tools, "editor:"+s.Meta.ID))
		if err != nil {
			return err
		}
		body = map[string]any{"revision": s.Meta.Revision, "content_version": s.Meta.ContentVersion, "messages": page, "next_cursor": next}
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}

type sharePatchRequest struct {
	Revision        int64                      `json:"expected_revision"`
	ContentVersion  int64                      `json:"expected_content_version,omitempty"`
	Remove          []string                   `json:"remove_message_ids,omitempty"`
	SnapshotID      string                     `json:"snapshot_id,omitempty"`
	SnapshotVersion string                     `json:"snapshot_version,omitempty"`
	Selection       *devicewire.ShareSelection `json:"selection,omitempty"`
	Future          *bool                      `json:"include_future,omitempty"`
	Tools           *bool                      `json:"show_tools,omitempty"`
	Expires         *time.Time                 `json:"expires_at,omitempty"`
	Ack             *shareAck                  `json:"acknowledgement,omitempty"`
}

func (d *Devices) sharePatch(w http.ResponseWriter, r *http.Request) error {
	var req sharePatchRequest
	if err := shareJSON(w, r, &req); err != nil {
		return err
	}
	if len(req.Remove) > 1000 || (req.Selection != nil && req.Remove != nil) {
		return shareFault{400, "invalid_selection"}
	}
	// A re-enable needs a fresh frontier; capture it before the writer fence.
	var futureOwner db.SessionShareOwnership
	var future *devicewire.ShareProjection
	if req.Future != nil && *req.Future {
		needsFrontier := false
		if err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
			_, s, err := d.manageShare(r.Context(), t, r.PathValue("share_id"))
			if err != nil {
				return err
			}
			if s.Meta.Status != "active" {
				return shareMissing()
			}
			if req.Revision != s.Meta.Revision || (req.ContentVersion != 0 && req.ContentVersion != s.Meta.ContentVersion) {
				return shareFail("revision_conflict")
			}
			needsFrontier = !s.Meta.Future || s.Meta.Publication == "paused"
			if !needsFrontier {
				return nil
			}
			if req.Ack == nil {
				return shareFail("preview_required")
			}
			p, err := targetPolicy(sharePolicy{Operation: "update", ShareID: s.Meta.ID, Revision: req.Revision, ContentVersion: req.ContentVersion, SnapshotID: req.SnapshotID, SnapshotVersion: req.SnapshotVersion, Selection: req.Selection, Future: req.Future, Tools: req.Tools, Expires: req.Expires}, &s)
			if err != nil {
				return err
			}
			if _, err = d.acknowledged(r.Context(), t, *req.Ack, p, s.Owner.Machine, s.Owner.SessionID); err != nil {
				return err
			}
			futureOwner = s.Owner
			return nil
		}); err != nil {
			return err
		}
		if needsFrontier {
			p, err := d.projectShare(r.Context(), futureOwner, nil)
			if err != nil {
				return err
			}
			future = &p
		}
	}
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		row, s, err := d.manageShare(r.Context(), t, r.PathValue("share_id"))
		if err != nil {
			return err
		}
		if s.Meta.Status != "active" {
			return shareMissing()
		}
		if req.Revision != s.Meta.Revision || (req.ContentVersion != 0 && req.ContentVersion != s.Meta.ContentVersion) {
			return shareFail("revision_conflict")
		}
		if (req.Selection == nil) != (req.SnapshotID == "") || (req.SnapshotID == "") != (req.SnapshotVersion == "") {
			return shareFault{400, "invalid_selection"}
		}
		if req.Expires != nil && (!req.Expires.After(time.Now()) || req.Expires.After(s.Meta.Created.Add(30*24*time.Hour))) {
			return shareFault{400, "invalid_expiry"}
		}
		widening := req.Selection != nil || (req.Tools != nil && *req.Tools && !s.Meta.Tools) || (req.Future != nil && *req.Future && (!s.Meta.Future || s.Meta.Publication == "paused")) || (req.Expires != nil && req.Expires.After(s.Meta.Expires))
		contentChanged := false
		if widening {
			if req.Ack == nil {
				return shareFail("preview_required")
			}
			if req.ContentVersion != s.Meta.ContentVersion {
				return shareFail("revision_conflict")
			}
			p, err := targetPolicy(sharePolicy{Operation: "update", ShareID: s.Meta.ID, Revision: req.Revision, ContentVersion: req.ContentVersion, SnapshotID: req.SnapshotID, SnapshotVersion: req.SnapshotVersion, Selection: req.Selection, Future: req.Future, Tools: req.Tools, Expires: req.Expires}, &s)
			if err != nil {
				return err
			}
			v, err := d.acknowledged(r.Context(), t, *req.Ack, p, s.Owner.Machine, s.Owner.SessionID)
			if err != nil {
				return err
			}
			if req.Selection != nil {
				s.Messages = append([]devicewire.ShareProjectionMessage(nil), v.Messages...)
				for i := range s.Messages {
					s.Messages[i].Message.ID = rand.Text()
				}
				s.Selection = *req.Selection
				s.Meta.Selection.Mode = req.Selection.Mode
				contentChanged = true
			}
		}
		if len(req.Remove) > 0 {
			remove := map[string]bool{}
			for _, id := range req.Remove {
				if id == "" || remove[id] {
					return shareFault{400, "invalid_selection"}
				}
				remove[id] = true
			}
			kept := []devicewire.ShareProjectionMessage{}
			for _, m := range s.Messages {
				id := shareHash([]byte("editor:" + s.Meta.ID + "/" + m.Message.ID))[:32]
				if remove[id] {
					delete(remove, id)
					s.Excluded[m.Identity] = true
				} else {
					kept = append(kept, m)
				}
			}
			if len(remove) != 0 {
				return shareFault{400, "invalid_selection"}
			}
			s.Messages = kept
			contentChanged = true
		}
		if req.Tools != nil {
			contentChanged = contentChanged || s.Meta.Tools != *req.Tools
			s.Meta.Tools = *req.Tools
		}
		if req.Expires != nil {
			s.Meta.Expires = *req.Expires
		}
		if req.Future != nil {
			reenable := *req.Future && (!s.Meta.Future || s.Meta.Publication == "paused")
			s.Meta.Future = *req.Future
			if reenable {
				if future == nil || futureOwner != s.Owner {
					return shareFail("share_changed")
				}
				applyShareFuture(&s, *future)
			} else if !*req.Future {
				s.Meta.Publication = "frozen"
				s.Meta.Pause = nil
			}
		}
		s.Meta.Selection.Count = len(s.Messages)
		s.Meta.Revision++
		if contentChanged {
			s.Meta.ContentVersion++
		}
		s.Meta.Updated = time.Now().UTC()
		if err = saveSealed(t, row, s); err != nil {
			return err
		}
		if widening {
			shareReport(w, s.Authority)
		}
		body = map[string]any{"share": s.Meta}
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}
func (d *Devices) shareRevoke(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Revision int64 `json:"expected_revision"`
	}
	if err := shareJSON(w, r, &req); err != nil {
		return err
	}
	var body any
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		row, s, err := d.manageShare(r.Context(), t, r.PathValue("share_id"))
		if err != nil {
			return err
		}
		if req.Revision != s.Meta.Revision {
			return shareFail("revision_conflict")
		}
		if s.Meta.Status != "revoked" {
			s.Meta.Status = "revoked"
			s.Meta.Publication = "frozen"
			s.Meta.Future = false
			s.Meta.Revision++
			s.Meta.Updated = time.Now().UTC()
			if err = saveSealed(t, row, s); err != nil {
				return err
			}
		}
		body = map[string]any{"share_id": s.Meta.ID, "revision": s.Meta.Revision, "status": "revoked"}
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}
func (d *Devices) shareReadPublic(w http.ResponseWriter, r *http.Request) error {
	token := r.PathValue("token")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	_, handle := d.sharingOrigin()
	if err != nil || len(raw) != 48 || base64.RawURLEncoding.EncodeToString(raw) != token || string(raw[:16]) != string(handle) {
		return shareMissing()
	}
	var body any
	err = d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		row, err := t.ByHash(shareHash([]byte(token)))
		if err != nil {
			return shareMissing()
		}
		_, s, err := loadSealed(t, row.ID)
		if err != nil {
			return shareMissing()
		}
		if s.Meta.Status != "active" || !s.Meta.Expires.After(time.Now()) {
			return shareMissing()
		}
		o, err := t.SessionShareOwner(s.Owner.Machine, s.Owner.SessionID)
		if err != nil || o == nil || *o != s.Owner {
			return shareMissing()
		}
		if _, err = d.requireAuthority(r.Context(), s.Owner); err != nil {
			return shareMissing()
		}
		page, next, err := sharePage(t, r, "public:"+row.Hash, s.Meta.ID, shareVersion(s), sharePublic(s.Messages, s.Meta.Tools, "public:"+s.Meta.ID))
		if err != nil {
			return err
		}
		poll := 30
		if s.Meta.Publication == "live" {
			poll = 15
		}
		body = map[string]any{"revision": s.Meta.Revision, "content_version": s.Meta.ContentVersion, "expires_at": s.Meta.Expires, "publication_status": s.Meta.Publication, "poll_after_seconds": poll, "messages": page, "next_cursor": next}
		shareReport(w, s.Authority)
		return nil
	})
	if err != nil {
		return err
	}
	return shareEncode(w, body)
}

// enableShareFuture always captures a fresh frontier. Unselected history and the
// interval since the preview (or since disabling) are excluded, never backfilled.
func (d *Devices) enableShareFuture(ctx context.Context, s *sealedShare) error {
	p, err := d.projectShare(ctx, s.Owner, nil)
	if err != nil {
		return err
	}
	applyShareFuture(s, p)
	return nil
}

// applyShareFuture installs a frontier captured outside the share transaction:
// node RPCs never run while the share writer fence is held.
func applyShareFuture(s *sealedShare, p devicewire.ShareProjection) {
	s.Frontier = p.Frontier
	s.WindowEOF = p.Frontier.EOF
	s.WindowSequence = p.Frontier.SubmissionSequence
	if s.Excluded == nil {
		s.Excluded = map[string]bool{}
	}
	for _, m := range p.Messages {
		s.Excluded[m.Identity] = true
	}
	for id := range p.Frontier.FirstSeen {
		s.Excluded[id] = true
	}
	s.Meta.Publication = "live"
	s.Meta.Pause = nil
}
func (d *Devices) ingestShare(ctx context.Context, id string) error {
	// Read the frontier, release the writer fence, call the node, then apply the
	// result only if nothing about the share changed in the meantime.
	var before sealedShare
	live := false
	if err := d.shareDB().ShareTransaction(ctx, func(t *db.ShareTx) error {
		_, s, err := loadSealed(t, id)
		if err != nil {
			return err
		}
		if s.Meta.Status != "active" || !s.Meta.Future || s.Meta.Publication != "live" {
			return nil
		}
		live = true
		before = s
		return t.MarkIngested(id)
	}); err != nil || !live {
		return err
	}
	p, err := d.projectShare(ctx, before.Owner, &before.Frontier)
	return d.shareDB().ShareTransaction(ctx, func(t *db.ShareTx) error {
		row, s, lerr := loadSealed(t, id)
		if lerr != nil {
			return lerr
		}
		if s.Meta.Revision != before.Meta.Revision || s.Meta.ContentVersion != before.Meta.ContentVersion ||
			s.Meta.Status != "active" || !s.Meta.Future || s.Meta.Publication != "live" || shareValueHash(s.Frontier) != shareValueHash(before.Frontier) {
			return nil
		}
		pause := func(reason string) error {
			// Rebuild from the committed row, so a failed oversized append never
			// contaminates the sealed history retained by a paused capability.
			if err := json.Unmarshal(row.Body, &s); err != nil {
				return err
			}
			s.Meta.Publication = "paused"
			s.Meta.Pause = &reason
			s.Meta.ContentVersion++
			s.Meta.Updated = time.Now().UTC()
			return saveSealed(t, row, s)
		}
		if err != nil {
			var f shareFault
			reason := "authority_unavailable"
			if errors.As(err, &f) {
				reason = f.code
			}
			return pause(reason)
		}
		known := map[string]bool{}
		for _, m := range s.Messages {
			known[m.Identity] = true
		}
		changed := false
		for _, m := range p.Messages {
			if known[m.Identity] || s.Excluded[m.Identity] {
				continue
			}
			eligible := m.FirstSeen >= s.WindowEOF
			if m.Submission {
				eligible = m.FirstSeen > s.WindowSequence
			}
			if !eligible {
				s.Excluded[m.Identity] = true
				continue
			}
			m.Message.ID = rand.Text()
			s.Messages = append(s.Messages, m)
			known[m.Identity] = true
			changed = true
		}
		// Persist first-seen pending identities even when their result is not yet public.
		s.Frontier = p.Frontier
		s.Meta.Selection.Count = len(s.Messages)
		if changed {
			s.Meta.ContentVersion++
			s.Meta.Updated = time.Now().UTC()
		}
		raw, _ := json.Marshal(s)
		if len(raw) > 16<<20 || len(s.Messages) > 10000 {
			return pause("source_limit")
		}
		return saveSealed(t, row, s)
	})
}
func (d *Devices) runShareIngestion(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if d.shareDB() == nil {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 12*time.Second)
			ids, err := d.shareDB().LiveShareIDs(c)
			cancel()
			if err != nil {
				continue
			}
			for _, id := range ids {
				if ctx.Err() != nil {
					return
				}
				c, cancel := context.WithTimeout(ctx, 12*time.Second)
				_ = d.ingestShare(c, id)
				cancel()
			}
		}
	}
}

type shareInspection struct {
	Snapshot shareSnapshot
	Admin    string
	ID       string
}

func (d *Devices) shareInspect(w http.ResponseWriter, r *http.Request) error {
	c := devicewire.CallerOf(r.Context())
	machine, session := r.PathValue("mid"), r.PathValue("sid")
	a, ok := d.currentShareAuthority(machine)
	if !ok || a.Issuer != c.Issuer || devicewire.ShareAuthorityOf(r.Context()) != a {
		return shareFail("authority_unavailable")
	}
	owner, err := d.shareDB().SessionShareOwner(r.Context(), machine, session)
	if err != nil {
		return err
	}
	o := db.SessionShareOwnership{Machine: machine, SessionID: session, Issuer: c.Issuer, Generation: a.Generation, Incarnation: rand.Text()}
	if owner != nil {
		o = *owner
	}
	p, err := d.projectShare(r.Context(), o, nil)
	if err != nil {
		return err
	}
	o.Incarnation = p.Frontier.Incarnation
	id := rand.Text()
	snap := shareSnapshot{Machine: machine, Session: session, Owner: o, Authority: a, Projection: p, Expires: time.Now().UTC().Add(5 * time.Minute)}
	snap.Version = shareValueHash(snap)
	var inspectionPage []devicewire.PublicShareMessage
	err = d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		if err := t.PutObject(id, "inspection", c.Issuer+"/"+c.CredentialID, snap.Expires, shareInspection{snap, c.CredentialID, id}); err != nil {
			return err
		}
		var err error
		inspectionPage, _, err = sharePage(t, r, c.Issuer+"/"+c.CredentialID, id, snap.Version, sharePublic(p.Messages, false, id))
		return err
	})
	if err != nil {
		return err
	}
	var subject any
	revision := int64(0)
	if owner != nil {
		subject = owner.Subject
		revision = owner.Revision
	}
	shareReport(w, a)
	return shareEncode(w, map[string]any{"verification_id": id, "verification_version": snap.Version, "incarnation": o.Incarnation, "owner_subject": subject, "owner_revision": revision, "source_kind": p.Frontier.Adapter, "complete": true, "message_count": len(p.Messages), "preview": inspectionPage, "expires_at": snap.Expires})
}
func (d *Devices) shareAttribute(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ID        string `json:"verification_id"`
		Version   string `json:"verification_version"`
		Subject   string `json:"owner_subject"`
		Revision  int64  `json:"expected_owner_revision"`
		Confirmed bool   `json:"confirmed"`
		Reason    string `json:"reason"`
	}
	if err := shareJSON(w, r, &req); err != nil {
		return err
	}
	c := devicewire.CallerOf(r.Context())
	if !req.Confirmed || req.Revision != 0 || req.Reason == "" || len(req.Reason) > 1000 || req.Subject != c.OwnerSubject || req.Subject == "" {
		return shareFault{400, "invalid_request"}
	}
	var inspection shareInspection
	err := d.shareDB().ShareTransaction(r.Context(), func(t *db.ShareTx) error {
		return t.Object(req.ID, "inspection", c.Issuer+"/"+c.CredentialID, &inspection)
	})
	if err != nil {
		return shareFail("inspection_changed")
	}
	snap := inspection.Snapshot
	if snap.Version != req.Version || snap.Machine != r.PathValue("mid") || snap.Session != r.PathValue("sid") || inspection.Admin != c.CredentialID {
		return shareFail("inspection_changed")
	}
	p, err := d.projectShare(r.Context(), snap.Owner, &snap.Projection.Frontier)
	if err != nil || shareValueHash(p) != shareValueHash(snap.Projection) {
		return shareFail("inspection_changed")
	}
	o := snap.Owner
	o.Subject = req.Subject
	o, err = d.shareDB().AttributeSession(r.Context(), o, c.CredentialID, req.Reason, shareValueHash(p.Frontier))
	if err != nil {
		return shareFail("ownership_conflict")
	}
	shareReport(w, snap.Authority)
	return shareEncode(w, map[string]any{"owner_subject": o.Subject, "owner_revision": o.Revision, "incarnation": o.Incarnation, "audit_id": o.AuditID})
}
