package extwire

// A manager's proof that a request with one of its extensions' tokens came through it (its relay to the hub,
// docs/assistant/managers.md "Extensions and the hub's API"). The hub takes an extension's token from the internet only with
// it, so a token that leaked is no use on its own. The proof is an HMAC keyed by ProofKey of the manager's bus token,
// which the hub derives from the token when the manager connects and keeps with that live connection only (its
// database has another hash of the token, which opens nothing here); it signs the request's method and URI, the
// extension's token (its hash), a time and a nonce. The hub takes it within ProofWindow of its own clock, and each
// nonce once. Neither side logs it; the hub drops it before anything else sees the request.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HeaderManagerProof carries the proof.
const HeaderManagerProof = "X-Persona-Manager-Proof"

// The hub's answers to a proof that is the manager's but not taken (docs/assistant/managers.md "Extensions and the hub's API").
const (
	CodeClockSkew     = "clock_skew"     // 403: its time is out of ProofWindow
	CodeProofReplayed = "proof_replayed" // 403: its nonce was taken already
	CodeProofBusy     = "proof_busy"     // 429: the manager has too many nonces kept just now
	// CodeManagerOffline (503): the manager is not on the hub's bus (the relay's own answer while it is not, and the
	// hub's to a proof that names a manager of its roster with no live connection).
	CodeManagerOffline = "manager_offline"
)

// HeaderAuthRefused is on a response the hub's own auth layer wrote refusing an extension's token as nobody's
// (RefusedToken, a 401) or its manager as not on the bus (RefusedManagerOffline, a 503 manager_offline). The hub
// takes it off every other response (a manager's or an extension's passed on), so a relay can tell the hub's refusal
// from a 401 that came from further on.
const HeaderAuthRefused = "X-Persona-Auth-Refused"

// The values of HeaderAuthRefused.
const (
	RefusedToken          = "token"
	RefusedManagerOffline = CodeManagerOffline
)

// ProofWindow is how far a proof's time may be from the hub's clock, either way.
const ProofWindow = 60 * time.Second

// proofVersion starts the header and the signed text.
const proofVersion = "v1"

// ProofKey is the key of the proofs of the manager whose bus token is busToken: an HMAC of a label keyed by the token,
// in hex. It is not the hash the hub keeps of the token (its database, a backup), so reading those forges no proof.
func ProofKey(busToken string) string {
	m := hmac.New(sha256.New, []byte(busToken))
	m.Write([]byte("persona-manager-proof/" + proofVersion))
	return hex.EncodeToString(m.Sum(nil))
}

// Proof is a parsed HeaderManagerProof.
type Proof struct {
	Manager string
	Time    time.Time
	Nonce   string
	sig     []byte
}

// SignProof is the header value that vouches for a request of method on requestURI (its path and query as sent) with
// the extension's token token, from manager, at now.
func SignProof(key, manager, method, requestURI, token string, now time.Time) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	nonce := base64.RawURLEncoding.EncodeToString(b)
	t := strconv.FormatInt(now.Unix(), 10)
	sig := proofMAC(key, manager, method, requestURI, token, t, nonce)
	return proofVersion + ",m=" + manager + ",t=" + t + ",n=" + nonce + ",s=" + base64.RawURLEncoding.EncodeToString(sig)
}

var (
	// Compiled on first use, not whenever a program that links the contract starts (a hook, #1308). A contract
	// depends on the standard library only, so not internal/lazyre.
	proofManager = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`) })
	proofNonce   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`) })
	errProof     = errors.New("not a manager's proof")
)

// ParseProof reads a header value (it checks only its shape: Valid checks the signature).
func ParseProof(v string) (Proof, error) {
	parts := strings.Split(v, ",")
	if len(parts) != 5 || parts[0] != proofVersion {
		return Proof{}, errProof
	}
	var p Proof
	var t string
	for i, name := range []string{"m=", "t=", "n=", "s="} {
		val, ok := strings.CutPrefix(parts[i+1], name)
		if !ok {
			return Proof{}, errProof
		}
		switch i {
		case 0:
			p.Manager = val
		case 1:
			t = val
		case 2:
			p.Nonce = val
		case 3:
			sig, err := base64.RawURLEncoding.DecodeString(val)
			if err != nil || len(sig) != sha256.Size {
				return Proof{}, errProof
			}
			p.sig = sig
		}
	}
	secs, err := strconv.ParseInt(t, 10, 64)
	if err != nil || secs <= 0 || strconv.FormatInt(secs, 10) != t || !proofManager().MatchString(p.Manager) ||
		!proofNonce().MatchString(p.Nonce) {
		return Proof{}, errProof
	}
	p.Time = time.Unix(secs, 0)
	return p, nil
}

// Valid says whether p was signed with key for a request of method on requestURI with token. The time window and the
// nonce are the caller's to check.
func (p Proof) Valid(key, method, requestURI, token string) bool {
	if key == "" || len(p.sig) == 0 {
		return false
	}
	want := proofMAC(key, p.Manager, method, requestURI, token, strconv.FormatInt(p.Time.Unix(), 10), p.Nonce)
	return hmac.Equal(want, p.sig)
}

// Fresh says whether p's time is within ProofWindow of now.
func (p Proof) Fresh(now time.Time) bool {
	d := now.Sub(p.Time)
	return d <= ProofWindow && d >= -ProofWindow
}

func proofMAC(key, manager, method, requestURI, token, t, nonce string) []byte {
	tok := sha256.Sum256([]byte(token))
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(strings.Join([]string{"persona-manager-proof/" + proofVersion, manager, method, requestURI,
		hex.EncodeToString(tok[:]), t, nonce}, "\n")))
	return m.Sum(nil)
}
