package link

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The connector's own TLS identity for passive mode (docs/operations/link.md "Passive mode"): an ECDSA P-256 key that never changes
// and a self-signed certificate renewed with it. The hub pins the key, the way the apps and persona-link pin the hub's,
// with the same fingerprint: SHA-256 of the SubjectPublicKeyInfo, base64url without padding. Renewing the certificate
// keeps the pin; a new key needs pairing again.

// tlsName is the only name the certificate carries: whoever reaches the port can read it, so neither the host name nor
// an address goes in (the hub's certificate does the same, hub/tlscert.go). Nobody checks it.
const tlsName = "connector.persona.invalid"

// Settings, not measurements: how long a certificate is valid and how long before its end it is renewed.
const (
	certLife  = 365 * 24 * time.Hour
	certRenew = 30 * 24 * time.Hour
)

// TLSDir is where the connector of the link.json at path keeps its identity: link-tls/ beside it.
func TLSDir(path string) string { return filepath.Join(filepath.Dir(path), "link-tls") }

// Pin is the fingerprint of a certificate's key.
func Pin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// identity is the key and certificate in dir, renewed when it is about to run out (a listener lives that long).
type identity struct {
	dir string
	mu  sync.Mutex
	key *ecdsa.PrivateKey
	crt *tls.Certificate
	fp  string
}

// Identity loads the connector's identity from dir, making the key the first time; fp is its pin.
func Identity(dir string) (fp string, err error) {
	id, err := loadIdentity(dir, time.Now())
	if err != nil {
		return "", err
	}
	return id.fp, nil
}

// ReadPin is the pin of the certificate in dir, without making anything: "" when there is none yet.
func ReadPin(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "link.crt"))
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return ""
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return Pin(leaf)
}

func loadIdentity(dir string, now time.Time) (*identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadOrMakeKey(filepath.Join(dir, "link.key"))
	if err != nil {
		return nil, err
	}
	id := &identity{dir: dir, key: key}
	if err := id.renew(now); err != nil {
		return nil, err
	}
	return id, nil
}

func loadOrMakeKey(file string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		// Linked into place, never over an existing key: two processes making one at once end up with the same.
		if err := WritePrivate(file, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), false); err != nil && !os.IsExist(err) {
			return nil, err
		}
		if b, err = os.ReadFile(file); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := os.Chmod(file, 0o600); err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("%s: not a PEM key", file)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%s: the identity key must be ECDSA P-256", file)
	}
	return key, nil
}

// renew reads the certificate on disk, or makes a new one for the same key when there is none, it is not this key's,
// or it runs out within certRenew.
func (id *identity) renew(now time.Time) error {
	file := filepath.Join(id.dir, "link.crt")
	if b, err := os.ReadFile(file); err == nil {
		if block, _ := pem.Decode(b); block != nil {
			if leaf, err := x509.ParseCertificate(block.Bytes); err == nil && leaf.PublicKey != nil &&
				id.key.PublicKey.Equal(leaf.PublicKey) && !now.Before(leaf.NotBefore) && leaf.NotAfter.After(now.Add(certRenew)) {
				id.crt = &tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: id.key, Leaf: leaf}
				id.fp = Pin(leaf)
				return nil
			}
		}
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "Persona connector"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(certLife),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, DNSNames: []string{tlsName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &id.key.PublicKey, id.key)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	if err := WritePrivate(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), true); err != nil {
		return err
	}
	id.crt = &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: id.key, Leaf: leaf}
	id.fp = Pin(leaf)
	return nil
}

// certificate is the certificate to serve now, renewed first when it is running out.
func (id *identity) certificate(now time.Time) (*tls.Certificate, error) {
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.crt == nil || !id.crt.Leaf.NotAfter.After(now.Add(certRenew)) {
		if err := id.renew(now); err != nil {
			return nil, err
		}
	}
	return id.crt, nil
}

// WritePrivate publishes a 0600 file whole: renamed over the old one when replace is set, else linked into place,
// where a file already there wins (an os.IsExist error), so concurrent creators of a key all use the one that won.
// The hub's TLS identity is written with it too.
func WritePrivate(path string, data []byte, replace bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if replace {
		return os.Rename(f.Name(), path)
	}
	return os.Link(f.Name(), path)
}
