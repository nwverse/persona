package extmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
)

func TestMarketClientCAFile(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/persona-extensions.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"market_version":1,"name":"Private test","extensions":[]}`))
	}))
	caPEM := marketTestTLSCertificate(t, srv)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	spec, err := extension.ParseMarket(srv.URL + "/persona-extensions.json")
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(client *http.Client) error {
		mk := &extension.Market{Spec: spec, Client: client}
		index, _, err := mk.Fetch(context.Background())
		if err == nil && index.Name != "Private test" {
			t.Fatalf("catalog name %q", index.Name)
		}
		return err
	}

	t.Run("unset keeps the system-only client", func(t *testing.T) {
		var baseRoots *x509.CertPool
		if tlsConfig := http.DefaultTransport.(*http.Transport).TLSClientConfig; tlsConfig != nil {
			baseRoots = tlsConfig.RootCAs
		}
		client, err := marketCAClient("")
		if err != nil {
			t.Fatal(err)
		}
		transport := client.Transport.(*http.Transport)
		var gotRoots *x509.CertPool
		if transport.TLSClientConfig != nil {
			if transport.TLSClientConfig.InsecureSkipVerify {
				t.Fatal("certificate verification is disabled")
			}
			gotRoots = transport.TLSClientConfig.RootCAs
		}
		if gotRoots != baseRoots {
			t.Fatal("unset changed the default root pool")
		}
		if err := fetch(client); err == nil {
			t.Fatal("a private test CA was trusted without configuration")
		} else {
			t.Logf("unset fetch failed as expected: %v", err)
		}
		if _, err := extension.ParseMarket("http://market.example/persona-extensions.json"); err == nil {
			t.Fatal("plaintext marketplace accepted")
		}
	})

	t.Run("configured appends the CA without weakening verification", func(t *testing.T) {
		caFile := filepath.Join(t.TempDir(), "market-ca.pem")
		if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		before, err := x509.SystemCertPool()
		if err != nil {
			t.Fatalf("system roots: %v", err)
		}
		block, _ := pem.Decode(caPEM)
		if block == nil {
			t.Fatal("test CA is not PEM")
		}
		added, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		client, err := marketCAClient(caFile)
		if err != nil {
			t.Fatal(err)
		}
		transport := client.Transport.(*http.Transport)
		if transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
			t.Fatal("the configured CA pool is missing")
		}
		if transport.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("certificate verification is disabled")
		}
		got := map[string]bool{}
		for _, subject := range transport.TLSClientConfig.RootCAs.Subjects() {
			got[string(subject)] = true
		}
		for _, subject := range before.Subjects() {
			if !got[string(subject)] {
				t.Fatal("a system root was replaced instead of appended to")
			}
		}
		if !got[string(added.RawSubject)] {
			t.Fatal("the private test CA was not added to the system roots")
		}
		if err := fetch(client); err != nil {
			t.Fatalf("configured private CA fetch: %v", err)
		}
		t.Log("configured private CA fetch succeeded with verification enabled")

		other, err := marketCAClient("")
		if err != nil {
			t.Fatal(err)
		}
		if err := fetch(other); err == nil {
			t.Fatal("the additional root escaped into another market client")
		} else {
			t.Logf("client without the option still rejects the private CA: %v", err)
		}
	})

	for _, tc := range []struct {
		name, contents, want string
	}{
		{name: "unreadable file", want: "read"},
		{name: "malformed PEM", contents: "not a certificate", want: "no PEM certificates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caFile := filepath.Join(t.TempDir(), "market-ca.pem")
			if tc.contents != "" {
				if err := os.WriteFile(caFile, []byte(tc.contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			client, err := marketCAClient(caFile)
			if client != nil || err == nil {
				t.Fatalf("invalid CA file fell back to a client: %v, %v", client, err)
			}
			if !strings.Contains(err.Error(), "extension_market.ca_file") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unclear CA error %q; want setting and %q", err, tc.want)
			}
			t.Logf("invalid CA file failed clearly: %v", err)
		})
	}
}

func marketCAClient(caFile string) (*http.Client, error) {
	h := &Manager{cfg: &Config{ExtensionMarket: ExtensionMarketConfig{CAFile: caFile}}, extensions: &extensions{}}
	return h.marketClient()
}

// marketTestTLSCertificate prepares a server with a leaf signed by a test-only self-signed CA. The listener already
// exists, so the leaf can carry its actual IP address without a fixed host or address in the fixture.
func marketTestTLSCertificate(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Persona test marketplace CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ip := srv.Listener.Addr().(*net.TCPAddr).IP
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Persona test marketplace"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{ip},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}}}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}
