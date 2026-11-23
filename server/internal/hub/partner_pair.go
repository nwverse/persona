package hub

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Pairing two partner hubs (docs/server/partner-hubs.md "Pairing"): hub B makes a one-time code good for 10 minutes
// (Settings, `personad hub partner code`), a link carrying B's addresses, hub_id and key pin; only B needs TLS, it is
// the one dialed. Hub A, given the code, connects to B pinned to that key, hands over a credential it issued for B
// (and its own pin and https addresses, if it has them), and gets B's credential for A in return. A dials B; B dials A
// only when A gave a pin and an address. A credential of kind and role partner opens /api/v1/partner/link and nothing
// else; only its hash is kept. Pairing never touches the mesh: a hub that is a mesh member, or a mesh peer of the
// other, refuses.

// partnerCode is a pairing code as the apps show it.
type partnerCode struct {
	Code      string    `json:"code"` // persona://partner?…
	ExpiresAt time.Time `json:"expires_at"`
}

// partnerInfo is what each side tells the other when pairing.
type partnerInfo struct {
	HubID     string   `json:"hub_id"`
	Name      string   `json:"name"`
	Version   string   `json:"version,omitempty"`
	FP        string   `json:"fp,omitempty"`
	TLSPort   int      `json:"tls_port,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"` // https only
	Token     string   `json:"token"`               // the credential the sender issued to the other hub
}

func (h *Hub) selfPartnerInfo() partnerInfo {
	port, fp := h.tlsPin()
	info := partnerInfo{HubID: h.hubID(), Name: h.hubName(), Version: h.version, FP: fp, TLSPort: port}
	if fp == "" {
		return info // no TLS: nothing to dial here
	}
	for _, e := range h.endpointList() {
		if e.Scheme == "https" {
			info.Endpoints = append(info.Endpoints, e.URL)
		}
	}
	return info
}

// canIssue: this hub can hand out a code: it has a TLS key and an https address to be dialed at.
func (info partnerInfo) canIssue() bool {
	return info.TLSPort != 0 && info.FP != "" && len(info.Endpoints) > 0
}

// partnerError is a pairing's refusal as the apps get it: a status, a code they can act on, the text.
type partnerError struct {
	status int
	code   string
	err    error
}

func (e *partnerError) Error() string           { return e.err.Error() }
func (e *partnerError) Unwrap() error           { return e.err }
func (e *partnerError) Text(l i18n.Lang) string { return i18n.Localize(e.err, l) }

func partnerRefusal(status int, code, key string, args ...i18n.Args) error {
	return &partnerError{status: status, code: code, err: i18n.Err(key, args...)}
}

var (
	errPartnersOff     = partnerRefusal(http.StatusForbidden, "partners_off", "hub-partner_pair.partners_off")
	errPartnerMember   = partnerRefusal(http.StatusConflict, "member_hub", "hub-partner_pair.member_hub")
	errPartnerNoTLS    = partnerRefusal(http.StatusConflict, "no_tls", "hub-partner_pair.no_tls")
	errPartnerOwnCode  = partnerRefusal(http.StatusConflict, "own_code", "hub-partner_pair.own_code")
	errPartnerLinked   = partnerRefusal(http.StatusConflict, "already_linked", "hub-partner_pair.already_linked")
	errPartnerPin      = partnerRefusal(http.StatusConflict, "pin_changed", "hub-partner_pair.pin_changed")
	errPartnerNoStore  = partnerRefusal(http.StatusServiceUnavailable, "no_database", "hub-partner_pair.no_database")
	errPartnerNotFound = partnerRefusal(http.StatusNotFound, "not_found", "hub-partner_pair.not_found")
	errPartnerLoading  = partnerRefusal(http.StatusServiceUnavailable, "not_ready", "hub-partner_pair.not_ready")
	// A pairing never replaces another: a code's holder names its own hub_id (and pin), so it would take a partner's
	// place. The owner unpairs first. Said by the pasting hub (the code's hub is its partner), and by the code's hub
	// to the one that brought the code (in that hub's owner's words, after "The other hub refused:": the code's hub
	// is the one to unpair on).
	errPartnerPaired     = partnerRefusal(http.StatusConflict, "already_paired", "hub-partner_pair.already_paired")
	errPartnerPairedHere = partnerRefusal(http.StatusConflict, "already_paired", "hub-partner_pair.already_paired_here")
	errPartnerPinHere    = partnerRefusal(http.StatusConflict, "pin_changed", "hub-partner_pair.pin_changed_here")
	// One pairing with a hub at a time on each side (pairingPartner), said the same two ways.
	errPartnerPairing     = partnerRefusal(http.StatusConflict, "pairing_in_progress", "hub-partner_pair.pairing_in_progress")
	errPartnerPairingHere = partnerRefusal(http.StatusConflict, "pairing_in_progress", "hub-partner_pair.pairing_in_progress_here")
)

// partnersReady refuses what this hub cannot pair as now: partner hubs off, no store, a mesh member, the pairings
// not read yet.
func (h *Hub) partnersReady() error {
	switch {
	case h.cfg == nil || !h.cfg.Partners.enabled():
		return errPartnersOff
	case h.partnerStore() == nil || h.store == nil:
		return errPartnerNoStore
	case h.isMember():
		return errPartnerMember
	case !h.partnersLoaded():
		return errPartnerLoading
	}
	return nil
}

// partnerNow is when a pairing is made, as Postgres keeps it (to the microsecond): the hub's copy and the database's
// say the same PairedAt.
func partnerNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// partnerSecret is a code's secret: jh_ and 64 lowercase hex digits.
var partnerSecret = lazyre.New(`^jh_[0-9a-f]{64}$`)

// partnerHubID is what a hub_id from another hub may be (a hub makes a UUID, public.go): it names a credential and a
// pairing and goes into paths, so nothing else is taken.
var partnerHubID = lazyre.New(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// partnerMaxEndpoints is the most https addresses kept and dialed for a partner (a code names at most 6).
const partnerMaxEndpoints = 8

// newPartnerCode makes a pairing code for this hub (the expired ones are pruned meanwhile).
func (h *Hub) newPartnerCode(ctx context.Context) (*partnerCode, error) {
	if err := h.partnersReady(); err != nil {
		return nil, err
	}
	info := h.selfPartnerInfo()
	if !info.canIssue() {
		return nil, errPartnerNoTLS
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	code := "jh_" + hex.EncodeToString(secret[:])
	now := time.Now().UTC()
	st := h.partnerStore()
	if err := st.PrunePartnerCodes(ctx, now); err != nil {
		h.logf("partners: prune codes: %v", err)
	}
	expires := now.Add(partnerCodeLife)
	if err := st.CreatePartnerCode(ctx, hashToken(code), expires); err != nil {
		return nil, err
	}
	q := url.Values{"hub": {info.HubID}, "name": {info.Name}, "fp": {info.FP}, "tls": {strconv.Itoa(info.TLSPort)}, "code": {code}}
	for _, e := range info.Endpoints[:min(len(info.Endpoints), 6)] {
		q.Add("url", e)
	}
	return &partnerCode{Code: "persona://partner?" + q.Encode(), ExpiresAt: expires}, nil
}

type parsedPartnerCode struct {
	hubID, name, fp, secret string
	endpoints               []string
}

func parsePartnerCode(raw string) (*parsedPartnerCode, error) {
	bad := func(key string) error { return partnerRefusal(http.StatusBadRequest, "bad_code", key) }
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "persona" || u.Host != "partner" {
		return nil, bad("hub-partner_pair.not_a_code")
	}
	q := u.Query()
	p := &parsedPartnerCode{hubID: q.Get("hub"), name: q.Get("name"), fp: q.Get("fp"), secret: q.Get("code")}
	if !partnerHubID.MatchString(p.hubID) || p.fp == "" || len(p.fp) > 128 || !partnerSecret.MatchString(p.secret) {
		return nil, bad("hub-partner_pair.incomplete")
	}
	if p.endpoints = partnerHTTPS(q["url"]); len(p.endpoints) == 0 {
		return nil, bad("hub-partner_pair.no_address")
	}
	return p, nil
}

// partnerHTTPS are the https candidates of eps, ranked and without duplicates, at most partnerMaxEndpoints: a partner
// is only ever dialed over TLS, and what the other hub names is bounded (a code, its pairing answer, its hello).
func partnerHTTPS(eps []string) []string {
	out := slices.DeleteFunc(rankEndpoints(eps), func(e string) bool {
		if !strings.HasPrefix(e, "https://") || len(e) > 512 {
			return true
		}
		u, err := url.Parse(e)
		return err != nil || u.Hostname() == "" || u.User != nil
	})
	return out[:min(len(out), partnerMaxEndpoints)]
}

// partnerName is a name another hub gave, as this hub keeps and shows it (partnerText, 64 characters).
func partnerName(s string) string { return partnerText(s, 64) }

// partnerText is short text another hub gave: one line of at most n characters, without control or bidirectional
// formatting characters (they would let it show as other text).
func partnerText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || bidiFormat(r) {
			return -1
		}
		return r
	}, s)
	return clip(oneLine(s), n)
}

// partnerCredName is what Settings › Devices shows for the credential issued to a partner.
func (h *Hub) partnerCredName(name, hubID string) string {
	if name = clip(partnerName(name), 60); name == "" {
		name = shortID(hubID)
	}
	return i18n.T(h.userLang(), "hub-partner_pair.credential_name", i18n.Args{"name": name})
}

// joinPartner pairs this hub with the hub whose code it was given (this hub is A, the one that dials).
func (h *Hub) joinPartner(ctx context.Context, raw string) (*db.PartnerHub, error) {
	code, err := parsePartnerCode(raw)
	if err != nil {
		return nil, err
	}
	if err := h.partnersReady(); err != nil {
		return nil, err
	}
	me := h.selfPartnerInfo()
	switch {
	case code.hubID == me.HubID:
		return nil, errPartnerOwnCode
	case h.meshPeer(code.hubID):
		return nil, errPartnerLinked
	}
	done, ok := h.pairingPartner(code.hubID)
	if !ok {
		return nil, errPartnerPairing
	}
	defer done() // after the save: the pairing is in place before another may start
	switch old, had := h.partnerRow(code.hubID); {
	case had && old.FP != "" && old.FP != code.fp:
		return nil, errPartnerPin
	case had: // a code (anyone's) never replaces a pairing, not even with its pin: the owner unpairs first
		return nil, errPartnerPaired
	}
	tok := "jd_" + randomHex(24)
	cred := db.DeviceToken{ID: "dev_" + randomHex(6), Name: h.partnerCredName(code.name, code.hubID), Kind: rolePartner, Role: rolePartner,
		MachineID: code.hubID}
	if err := h.store.CreateDeviceToken(ctx, cred, hashToken(tok)); err != nil {
		return nil, err
	}
	h.partnerJoinStep(partnerJoinIssued)
	me.Token = tok
	body, _ := json.Marshal(me)
	lang := h.contextLang(ctx)
	got, err := h.partnerPost(ctx, code.fp, code.endpoints, code.secret, body, lang)
	if err == nil && got.HubID != code.hubID {
		err = partnerRefusal(http.StatusBadGateway, "wrong_hub", "hub-partner_pair.wrong_hub",
			i18n.Args{"got": strconv.Quote(got.HubID), "want": strconv.Quote(code.hubID)})
	}
	if err != nil {
		_, _ = h.store.RevokeDeviceToken(context.WithoutCancel(ctx), cred.ID)
		return nil, err
	}
	h.partnerJoinStep(partnerJoinAnswered)
	sealed, err := h.sealKey(got.Token)
	if err != nil {
		_, _ = h.store.RevokeDeviceToken(context.WithoutCancel(ctx), cred.ID)
		return nil, err
	}
	name := cmp.Or(partnerName(got.Name), partnerName(code.name))
	p := db.PartnerHub{HubID: got.HubID, Name: name, FP: code.fp, Endpoints: partnerHTTPS(slices.Concat(code.endpoints, got.Endpoints)),
		Token: sealed, CredentialID: cred.ID, Dial: assistantwire.PartnerDialAuto, CreatedAt: partnerNow()}
	if err := h.savePartner(ctx, p); err != nil {
		_, _ = h.store.RevokeDeviceToken(context.WithoutCancel(ctx), cred.ID)
		return nil, err
	}
	h.logf("partners: paired with %q (%s)", p.Name, p.HubID)
	return &p, nil
}

// partnerPost is the pairing request to the code's hub: through the same proxy-aware pinned dial as the link
// (partner_proxy.go), every candidate raced. A refusal keeps the other hub's code and status; 401 (the code is
// unknown, used or expired) is said in this hub's words.
func (h *Hub) partnerPost(ctx context.Context, fp string, eps []string, secret string, body []byte, lang i18n.Lang) (partnerInfo, error) {
	cands, err := h.partnerCandidates(eps)
	if err != nil {
		return partnerInfo{}, &partnerError{status: http.StatusBadGateway, code: "unreachable", err: err}
	}
	got, _, err := raceEndpoints(ctx, partnerKeys(cands), func(ctx context.Context, key string) (partnerInfo, error) {
		c := partnerCandidateOf(cands, key)
		client := h.partnerClient(fp, c.via)
		defer client.CloseIdleConnections()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+partnerPairPath, bytes.NewReader(body))
		if err != nil {
			return partnerInfo{}, err
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		if lang != "" {
			req.Header.Set("Accept-Language", string(lang)) // its refusal in this hub's user's words
		}
		resp, err := client.Do(req)
		if err != nil {
			return partnerInfo{}, c.failed(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode != http.StatusOK {
			return partnerInfo{}, partnerAnswered(resp.StatusCode, b)
		}
		var out partnerInfo
		if err := json.Unmarshal(b, &out); err != nil || !strings.HasPrefix(out.Token, "jd_") {
			return partnerInfo{}, partnerRefusal(http.StatusBadGateway, "no_credentials", "hub-partner_pair.no_credentials")
		}
		return out, nil
	}, func(partnerInfo) {})
	if err != nil {
		var pe *partnerError // the other hub answered: its word, not every candidate's
		if errors.As(err, &pe) {
			return partnerInfo{}, pe
		}
		return partnerInfo{}, &partnerError{status: http.StatusBadGateway, code: "unreachable", err: &partnerDialError{err}}
	}
	return got, nil
}

// partnerPassedCodes are the other hub's refusal codes passed on as they are (the apps act on them); any other is
// "refused".
var partnerPassedCodes = map[string]bool{"partners_off": true, "member_hub": true, "already_linked": true, "pin_changed": true,
	"already_paired": true, "pairing_in_progress": true, "tls_required": true, "no_database": true}

// partnerAnswered is the code's hub refusing: 401 is the code (in this hub's words), a 4xx its own code and words,
// anything else that it could not pair.
func partnerAnswered(status int, b []byte) error {
	var refusal struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(b, &refusal)
	why := apierr.Reason(b) // the other hub's cause, not the summary it localized it into (#5728)
	if why == "" {
		why = http.StatusText(status)
	}
	why = partnerText(why, 300) // the other hub's words, shown here: one bounded line
	switch {
	case status == http.StatusUnauthorized:
		return partnerRefusal(http.StatusUnauthorized, "code_refused", "hub-partner_pair.code_invalid")
	case status >= 400 && status < 500 && partnerPassedCodes[refusal.Code]:
		return &partnerError{status: http.StatusConflict, code: refusal.Code, err: i18n.Err("hub-partner_pair.refused", i18n.Args{"err": why})}
	}
	return &partnerError{status: http.StatusBadGateway, code: "refused", err: i18n.Err("hub-partner_pair.refused", i18n.Args{"err": why})}
}

// savePartner keeps a new pairing and starts dialing it; errPartnerPaired, nothing changed, when that hub is a
// partner already (a pairing is never replaced).
func (h *Hub) savePartner(ctx context.Context, p db.PartnerHub) error {
	if err := h.partnerStore().SavePartnerHub(ctx, p); errors.Is(err, db.ErrPartnerPaired) {
		return errPartnerPaired
	} else if err != nil {
		return err
	}
	h.partnerSaved(p)
	return nil
}

// partnerSaved puts a pairing into the running state: the old link (other credentials) goes, the dialer starts.
func (h *Hub) partnerSaved(p db.PartnerHub) {
	ps := &h.partners
	ps.mu.Lock()
	if ps.rows == nil {
		ps.rows = map[string]db.PartnerHub{}
	}
	if old, had := ps.rows[p.HubID]; had && p.SeenAt == nil {
		p.SeenAt = old.SeenAt
	}
	ps.rows[p.HubID] = p
	delete(ps.dial, p.HubID)
	old := ps.links[p.HubID]
	ps.mu.Unlock()
	if old != nil {
		old.close(partnerCloseDuplicate, "paired again")
	}
	h.stopPartnerDialer(p.HubID) // a dialer of the old pairing has its credential
	h.startPartnerDialer(p.HubID)
	h.partnersChanged()
}

// pairingPartner marks a pairing with hubID as being made here (joinPartner, servePartnerPair) until done is called;
// false, nothing marked, when one is already. Two at once must not run: when both owners paste each other's codes at
// the same moment, each hub's redeem revokes the partner credentials it issued the other hub before, among them the
// one its own join is handing over, and both hubs end up holding credentials the other revoked. So a hub joining
// another refuses that hub's pairing request, and the reverse; a refused or failed attempt revokes only its own
// credential.
func (h *Hub) pairingPartner(hubID string) (done func(), ok bool) {
	ps := &h.partners
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.pairing[hubID] {
		return nil, false
	}
	if ps.pairing == nil {
		ps.pairing = map[string]bool{}
	}
	ps.pairing[hubID] = true
	return func() {
		ps.mu.Lock()
		delete(ps.pairing, hubID)
		ps.mu.Unlock()
	}, true
}

// The steps of joinPartner a test may hold it at (partnerState.joinStep): its credential for the other hub issued
// (not sent yet), and the other hub's answer in (not saved yet).
const (
	partnerJoinIssued   = "issued"
	partnerJoinAnswered = "answered"
)

func (h *Hub) partnerJoinStep(step string) {
	h.partners.mu.Lock()
	hook := h.partners.joinStep
	h.partners.mu.Unlock()
	if hook != nil {
		hook(step)
	}
}

// meshPeer: hubID is a mesh peer of this hub (paired as linked hubs): it cannot be a partner too.
func (h *Hub) meshPeer(hubID string) bool {
	_, ok := h.pairedWith(hubID)
	return ok
}

// servePartnerPair is POST /api/v1/partner/pair: another hub (A) brings this hub's code (this hub is B).
func (h *Hub) servePartnerPair(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r.Context())
	if p.Role != rolePartnerPair || p.PairHash == "" {
		writeErr(w, http.StatusForbidden, errors.New("pairing partner hubs needs a partner pairing code"))
		return
	}
	if r.TLS == nil {
		h.securityError(w, r, http.StatusForbidden, "tls_required", 0)
		return
	}
	if err := h.partnersReady(); err != nil {
		writePartnerErr(w, err)
		return
	}
	var req partnerInfo
	if err := decodeJSONBody(w, r, &req, 64<<10); err != nil {
		writeBodyErr(w, err, err)
		return
	}
	me := h.selfPartnerInfo()
	if !partnerHubID.MatchString(req.HubID) || req.HubID == me.HubID || !strings.HasPrefix(req.Token, "jd_") || len(req.Token) > 256 ||
		len(req.FP) > 128 {
		writeErr(w, http.StatusBadRequest, errors.New("hub_id and token are required, and the hub_id must be another hub's"))
		return
	}
	// Whoever holds a code must not take over a partner's place by claiming its hub_id: the hub_id and pin are its own
	// word (it never shows it holds that key, and a hub's pin is no secret), so a pairing is never replaced, with that
	// pin or without one. The owner unpairs first.
	if h.meshPeer(req.HubID) {
		writePartnerErr(w, errPartnerLinked)
		return
	}
	// This hub pairing with that one meanwhile (its owner pasted that hub's code): refused, the code kept.
	done, ok := h.pairingPartner(req.HubID)
	if !ok {
		writePartnerErr(w, errPartnerPairingHere)
		return
	}
	defer done()
	old, had := h.partnerRow(req.HubID)
	switch {
	case had && old.FP != "" && old.FP != req.FP:
		writePartnerErr(w, errPartnerPinHere)
		return
	case had:
		writePartnerErr(w, errPartnerPairedHere)
		return
	}
	sealed, err := h.sealKey(req.Token)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	eps := partnerHTTPS(req.Endpoints)
	dial := assistantwire.PartnerDialNever // it dials this hub; this one dials back only where it can
	if req.FP != "" && len(eps) > 0 {
		dial = assistantwire.PartnerDialAuto
	}
	tok := "jd_" + randomHex(24)
	cred := db.DeviceToken{ID: "dev_" + randomHex(6), Name: h.partnerCredName(req.Name, req.HubID), Kind: rolePartner, Role: rolePartner,
		MachineID: req.HubID}
	row := db.PartnerHub{HubID: req.HubID, Name: partnerName(req.Name), FP: req.FP, Endpoints: eps, Token: sealed,
		CredentialID: cred.ID, Dial: dial, CreatedAt: partnerNow()}
	redeemed, err := h.partnerStore().RedeemPartnerCode(r.Context(), p.PairHash, row, cred, hashToken(tok))
	if errors.Is(err, db.ErrPartnerPaired) { // paired meanwhile (another code): the database has the last word
		writePartnerErr(w, errPartnerPairedHere)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !redeemed {
		h.securityError(w, r, http.StatusUnauthorized, "", 0)
		return
	}
	h.partnerSaved(row)
	h.logf("partners: paired with %q (%s), dial %s", row.Name, row.HubID, dial)
	me.Token = tok
	writeJSON(w, me)
}

// unpairPartner ends a pairing from this side: the other hub told over the link (when it is up), the pairing
// forgotten, the dialing stopped, the credential this hub issued revoked, the link closed (4012).
func (h *Hub) unpairPartner(ctx context.Context, hubID string, tell bool) (bool, error) {
	row, ok := h.partnerRow(hubID)
	if !ok {
		return false, nil
	}
	if tell {
		if l := h.partnerLinkTo(hubID); l != nil {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_ = l.rpc.Call(cctx, "unpair", map[string]any{}, nil)
			cancel()
		}
	}
	if st := h.partnerStore(); st != nil {
		if _, err := st.DeletePartnerHub(ctx, hubID); err != nil {
			return false, err
		}
	}
	ps := &h.partners
	ps.mu.Lock()
	delete(ps.rows, hubID)
	delete(ps.dial, hubID)
	delete(ps.heard, hubID)
	l := ps.links[hubID]
	ps.mu.Unlock()
	// The link first: a link this hub dialed ends with its dialer's context, which would close it 1001 (stopping)
	// before the 4012 that tells the partner to stop dialing.
	if l != nil {
		l.close(partnerCloseUnpaired, "unpaired")
	}
	h.stopPartnerDialer(hubID)
	if row.CredentialID != "" && h.store != nil {
		if _, err := h.store.RevokeDeviceToken(ctx, row.CredentialID); err != nil {
			h.logf("partners: revoke the credential of %s: %v", hubID, err)
		}
		h.forgetToken(row.CredentialID)
	}
	h.partnersChanged()
	h.logf("partners: unpaired %q (%s)", row.Name, hubID)
	return true, nil
}

// partnerUnpaired: the partner ended the pairing; this side forgets it too.
func (h *Hub) partnerUnpaired(hubID string) {
	if _, err := h.unpairPartner(context.Background(), hubID, false); err != nil {
		h.logf("partners: %s unpaired this hub: %v", hubID, err)
	}
}

// partnerPairPrincipal is the principal of a partner pairing code (jh_…), valid until used or expired.
func (h *Hub) partnerPairPrincipal(ctx context.Context, tok string) (principal, bool) {
	st := h.partnerStore()
	if st == nil || !partnerSecret.MatchString(tok) {
		return principal{}, false
	}
	hash := hashToken(tok)
	ok, err := st.PartnerCodeValid(ctx, hash)
	if err != nil || !ok {
		return principal{}, false
	}
	return principal{ID: "partnerpair_" + hash[:12], Role: rolePartnerPair, PairHash: hash}, true
}
