// Package tlsxtest mints a throwaway CA and leaves from it, so a test can
// ask for exactly the wrong certificate — a mismatched CA, an elapsed
// NotAfter, a SAN that doesn't match — without a PEM fixture in git. A
// committed fixture expires, and on the day it does the failure looks like a
// bug in the code under test rather than a bug in the calendar.
package tlsxtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/tlsx"
)

// CA is a self-signed root generated fresh per test, never written outside
// the test's own temp directory.
type CA struct {
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	caFile string
}

// NewCA generates a fresh ECDSA P-256 root and writes its certificate under
// t.TempDir(), ready to sign leaves via Leaf.
func NewCA(t *testing.T) *CA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(t),
		Subject:               pkix.Name{CommonName: "tlsxtest CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}

	caFile := filepath.Join(t.TempDir(), "ca.crt")
	writePEM(t, caFile, "CERTIFICATE", der)

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &CA{cert: cert, key: key, caFile: caFile}
}

// LeafOption customizes the certificate template Leaf mints.
type LeafOption func(*x509.Certificate)

// Expired backdates NotBefore and NotAfter so the resulting leaf has already
// lapsed.
func Expired() LeafOption {
	return func(c *x509.Certificate) {
		c.NotBefore = time.Now().Add(-2 * time.Hour)
		c.NotAfter = time.Now().Add(-time.Hour)
	}
}

// SANEmail sets the leaf's email Subject Alternative Names — the field R6
// maps NATS identity on, checked before the Subject DN.
func SANEmail(addrs ...string) LeafOption {
	return func(c *x509.Certificate) {
		c.EmailAddresses = addrs
	}
}

// IPSAN sets the leaf's IP Subject Alternative Names — a server leaf dialed
// by loopback address (an embedded test server has no DNS name) needs one,
// because x509 verification checks a dialed IP against IPAddresses and never
// falls back to DNSNames the way it does for a hostname.
func IPSAN(ips ...net.IP) LeafOption {
	return func(c *x509.Certificate) {
		c.IPAddresses = ips
	}
}

// Leaf mints a certificate for cn signed by ca, writes its cert and key
// under t.TempDir(), and returns a Config pointing at them and at ca's own
// certificate. cn is both the CommonName and the leaf's only DNS SAN.
func (ca *CA) Leaf(t *testing.T, cn string, opts ...LeafOption) tlsx.Config {
	t.Helper()

	der, keyDER := ca.mintLeaf(t, cn, opts...)

	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "EC PRIVATE KEY", keyDER)

	return tlsx.Config{CertFile: certFile, KeyFile: keyFile, CAFile: ca.caFile}
}

// Rotate overwrites the cert and key files named in cfg with a freshly
// minted leaf for cn, simulating cert-manager rewriting a mounted Secret in
// place, and advances their mtime so a caching loader is guaranteed to
// notice the change.
func (ca *CA) Rotate(t *testing.T, cfg tlsx.Config, cn string, opts ...LeafOption) {
	t.Helper()

	der, keyDER := ca.mintLeaf(t, cn, opts...)
	writePEM(t, cfg.CertFile, "CERTIFICATE", der)
	writePEM(t, cfg.KeyFile, "EC PRIVATE KEY", keyDER)

	future := time.Now().Add(time.Second)
	if err := os.Chtimes(cfg.CertFile, future, future); err != nil {
		t.Fatalf("advance cert mtime: %v", err)
	}
}

func (ca *CA) mintLeaf(t *testing.T, cn string, opts ...LeafOption) (certDER, keyDER []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: newSerial(t),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	for _, opt := range opts {
		opt(tmpl)
	}

	certDER, err = x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	keyDER, err = x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	return certDER, keyDER
}

func newSerial(t *testing.T) *big.Int {
	t.Helper()

	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	return serial
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()

	block := &pem.Block{Type: blockType, Bytes: der}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
