package assistant

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
)

const ownerIdentitiesFile = ".owner-identities.enc"
const ownerIdentityDomain = "persona.kb.owner-identities.v1"

// kbOwnerState is separate from people metadata, prompts, search and the hub's Profile snapshot. Only ciphertext
// goes to disk. Without handles.key, observations live for this assistant process only.
type kbOwnerState struct {
	mu      sync.Mutex
	key     []byte
	err     error
	sources map[string][]kb.Handle
}

func ownerAliases(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// ownerHandle uses the same forms as KB identity matching, without persisting a raw hint in an entity.
func ownerHandle(h kb.Handle) kb.Handle {
	h.Kind, h.Value = strings.TrimSpace(h.Kind), strings.TrimSpace(h.Value)
	if h.Kind == kb.HandlePhone {
		var b strings.Builder
		for i, r := range h.Value {
			if r >= '0' && r <= '9' || r == '+' && i == 0 {
				b.WriteRune(r)
			}
		}
		h.Value = b.String()
		if strings.Trim(h.Value, "+") == "" {
			h.Value = ""
		}
	} else if h.Kind == kb.HandleEmail || h.Kind == kb.HandleGitHub {
		h.Value = strings.ToLower(h.Value)
	}
	return h
}

func mergeOwnerHandles(a, b []kb.Handle) []kb.Handle {
	out := slices.Clone(a)
	for _, h := range b {
		h = ownerHandle(h)
		if h.Kind != "" && h.Value != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func ownerCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// kbOwnerOpen runs after taking the KB directory lock, before accepting observations. The HMAC domain separates
// this encryption key from the matching key, so exposing an encrypted owner record cannot reveal person hints.
func (s *Service) kbOwnerOpen(handleKey []byte) (err error) {
	o := &s.kb.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	defer func() { o.err = err }()
	if len(handleKey) == 0 {
		if _, err := os.Stat(filepath.Join(s.kb.dir, ownerIdentitiesFile)); err == nil {
			return errors.New("owner identities: key unavailable")
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("owner identities: encrypted record could not be read")
		}
		return nil
	}
	mac := hmac.New(sha256.New, handleKey)
	_, _ = mac.Write([]byte(ownerIdentityDomain))
	key := mac.Sum(nil)
	aead, err := ownerCipher(key)
	if err != nil {
		return errors.New("owner identities: encryption unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(s.kb.dir, ownerIdentitiesFile))
	if errors.Is(err, os.ErrNotExist) {
		o.key, o.sources = key, map[string][]kb.Handle{}
		return nil
	}
	if err != nil {
		return errors.New("owner identities: encrypted record could not be read")
	}
	if len(raw) < 1+aead.NonceSize()+aead.Overhead() || raw[0] != 1 {
		return errors.New("owner identities: invalid encrypted record")
	}
	plain, err := aead.Open(nil, raw[1:1+aead.NonceSize()], raw[1+aead.NonceSize():], []byte(ownerIdentityDomain))
	if err != nil {
		return errors.New("owner identities: encrypted record could not be opened")
	}
	var sources map[string][]kb.Handle
	if json.Unmarshal(plain, &sources) != nil {
		return errors.New("owner identities: invalid record")
	}
	if sources == nil {
		sources = map[string][]kb.Handle{}
	}
	o.key, o.sources = key, sources
	return nil
}

// saveOwnerLocked writes one complete private record atomically. It never writes plaintext, even temporarily.
func (s *Service) saveOwnerLocked(sources map[string][]kb.Handle) error {
	o := &s.kb.owner
	if len(o.key) == 0 {
		return nil
	}
	aead, err := ownerCipher(o.key)
	if err != nil {
		return errors.New("owner identities: encryption unavailable")
	}
	plain, err := json.Marshal(sources)
	if err != nil {
		return errors.New("owner identities: record could not be encoded")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return errors.New("owner identities: nonce unavailable")
	}
	raw := append([]byte{1}, nonce...)
	raw = aead.Seal(raw, nonce, plain, []byte(ownerIdentityDomain))
	f, err := os.CreateTemp(s.kb.dir, ".owner-identities-*.enc")
	if err != nil {
		return errors.New("owner identities: encrypted record could not be saved")
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(s.kb.dir, ownerIdentitiesFile))
	}
	if err != nil {
		return errors.New("owner identities: encrypted record could not be saved")
	}
	return nil
}

// kbObserveOwner sees only sender identity hints of messages explicitly marked Mine, never their text. Names from
// a connector do not change the user's editable aliases or the configured owner name.
func (s *Service) kbObserveOwner(source, _ string, handles []kb.Handle) {
	if source == "" {
		return
	}
	o := &s.kb.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return
	}
	merged := mergeOwnerHandles(o.sources[source], handles)
	if slices.Equal(merged, o.sources[source]) {
		return
	}
	next := cloneOwnerSources(o.sources)
	next[source] = merged
	if err := s.saveOwnerLocked(next); err != nil {
		s.logf("kb: owner identities could not be saved")
		return
	}
	o.sources = next
}

func cloneOwnerSources(in map[string][]kb.Handle) map[string][]kb.Handle {
	out := map[string][]kb.Handle{}
	for source, handles := range in {
		out[source] = slices.Clone(handles)
	}
	return out
}

func (s *Service) kbForgetOwner(source string) error {
	o := &s.kb.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	if _, ok := o.sources[source]; !ok {
		return nil
	}
	next := cloneOwnerSources(o.sources)
	delete(next, source)
	if err := s.saveOwnerLocked(next); err != nil {
		return err
	}
	o.sources = next
	return nil
}

func (s *Service) kbOwner(ctx context.Context) (assistantwire.KBOwner, error) {
	out := assistantwire.KBOwner{Name: s.owner(ctx), Aliases: []string{}, Handles: []kb.Handle{}}
	if _, err := s.kbGet(ctx); err != nil {
		return out, err
	}
	settings, ok := s.savedSettings(ctx)
	if !ok {
		return out, errors.New("owner aliases could not be read")
	}
	out.Aliases = ownerAliases(settings.OwnerAliases)
	o := &s.kb.owner
	o.mu.Lock()
	if o.err != nil {
		err := o.err
		o.mu.Unlock()
		return out, err
	}
	for _, handles := range o.sources {
		out.Handles = mergeOwnerHandles(out.Handles, handles)
	}
	o.mu.Unlock()
	if s.kbReadOnly(ctx) {
		return out, nil
	}
	var live assistantwire.KBOwnerSources
	if err := s.appCall(ctx, AppConnectors, assistantwire.ConnectorsOwner, map[string]any{}, &live); err != nil {
		return out, err
	}
	for _, src := range live.Sources {
		out.Handles = mergeOwnerHandles(out.Handles, src.Handles)
	}
	return out, nil
}
