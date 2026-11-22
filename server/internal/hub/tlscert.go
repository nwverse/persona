package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/link"
)

func loadTLSFiles(certFile, keyFile string) (tls.Certificate, string, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", err
	}
	cert.Leaf = leaf
	return cert, link.Pin(leaf), nil
}

// loadOrCreateTLS renews the certificate, never the identity key.
func loadOrCreateTLS(dir string, names []string, now time.Time) (tls.Certificate, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	keyFile, certFile := filepath.Join(dir, "hub.key"), filepath.Join(dir, "hub.crt")
	_, err := os.Stat(keyFile)
	if os.IsNotExist(err) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if err := link.WritePrivate(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), false); err != nil && !os.IsExist(err) {
			return tls.Certificate{}, "", err
		}
	} else if err != nil {
		return tls.Certificate{}, "", err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return tls.Certificate{}, "", fmt.Errorf("invalid TLS identity PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return tls.Certificate{}, "", fmt.Errorf("TLS identity must be ECDSA P-256")
	}
	dns, ips := certificateSANs(names)
	if cert, fp, err := loadTLSFiles(certFile, keyFile); err == nil {
		if !now.Before(cert.Leaf.NotBefore) && cert.Leaf.NotAfter.After(now.Add(30*24*time.Hour)) && sameSANs(cert.Leaf, dns, ips) {
			return cert, fp, nil
		}
	} else if _, statErr := os.Stat(certFile); statErr != nil && !os.IsNotExist(statErr) {
		return tls.Certificate{}, "", statErr
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "Persona hub"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, DNSNames: dns, IPAddresses: ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := link.WritePrivate(certFile, certPEM, true); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	cert.Leaf, err = x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, link.Pin(cert.Leaf), nil
}

func certificateSANs(names []string) ([]string, []net.IP) {
	dns := []string{}
	ips := []net.IP{}
	names = slices.Clone(names)
	slices.Sort(names)
	for _, name := range slices.Compact(names) {
		if ip := net.ParseIP(name); ip != nil {
			ips = append(ips, ip)
		} else if name != "" {
			dns = append(dns, name)
		}
	}
	return dns, ips
}

func sameSANs(cert *x509.Certificate, dns []string, ips []net.IP) bool {
	return slices.Equal(cert.DNSNames, dns) && slices.EqualFunc(cert.IPAddresses, ips, func(a, b net.IP) bool { return a.Equal(b) })
}

// tlsNames is the only name the self-signed certificate carries. Clients pin its key and check the hub id, never the
// names, and anyone who reaches the public port can read the certificate: the host name and the LAN addresses must
// not be in it (the addresses are only handed out after authentication, hub-changes.md 1.2).
func (h *Hub) tlsNames() []string {
	return []string{h.hubID() + ".persona.invalid"}
}
