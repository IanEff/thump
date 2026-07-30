// Package tlsx is the one place a *tls.Config is built — internal/httpx's
// sibling, and for the same reason. A TLS config assembled at the call site
// is a chance to get the root pool, the minimum version, or client-cert
// verification wrong, and every one of those mistakes succeeds at runtime
// instead of failing.
package tlsx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
)

// Config is the PEM triple a beat reads from its environment. All three
// empty is a legal state, not an error — the offline path has no peer to
// authenticate to. CAFile without Cert/Key is one-way TLS: verify the peer,
// present nothing.
type Config struct {
	CertFile string // TLS_CERT_FILE — this beat's own leaf
	KeyFile  string // TLS_KEY_FILE
	CAFile   string // TLS_CA_FILE — the issuer both ends verify against
}

// Client returns a config that verifies the peer against CAFile and presents
// CertFile/KeyFile when both are set. The certificate is reread from disk on
// every handshake behind an mtime check, so a rotated file is picked up
// without a process restart.
func Client(c Config) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}

	if c.CAFile != "" {
		pool, err := loadCAPool(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tlsx: client: %w", err)
		}
		cfg.RootCAs = pool
	}

	if c.CertFile != "" && c.KeyFile != "" {
		reloader := &certReloader{certFile: c.CertFile, keyFile: c.KeyFile}
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return reloader.load()
		}
	}

	return cfg, nil
}

// Server returns a config that presents CertFile/KeyFile and requires and
// verifies a client certificate against CAFile. CAFile is mandatory here —
// ClientAuth is always RequireAndVerifyClientCert, and a nil ClientCAs pool
// would mean every client cert fails verification, not that none is
// required.
func Server(c Config) (*tls.Config, error) {
	if c.CertFile == "" || c.KeyFile == "" {
		return nil, fmt.Errorf("tlsx: server: CertFile and KeyFile are required")
	}
	if c.CAFile == "" {
		return nil, fmt.Errorf("tlsx: server: CAFile is required to verify a client certificate")
	}

	pool, err := loadCAPool(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("tlsx: server: %w", err)
	}

	reloader := &certReloader{certFile: c.CertFile, keyFile: c.KeyFile}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientCAs:  pool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return reloader.load()
		},
	}, nil
}

// loadCAPool reads a PEM file into a fresh pool and refuses anything
// AppendCertsFromPEM doesn't accept — its return is a bool, not an error, and
// ignoring it turns a garbage or empty CA file into a pool that silently
// trusts nothing.
func loadCAPool(caFile string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(caFile) //nolint:gosec // G304: operator-supplied CA path, not user input
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s contains no usable certificates", caFile)
	}
	return pool, nil
}

// certReloader rereads a keypair from disk when its file's mtime advances,
// so cert-manager rewriting the files in place rotates the certificate
// without a pod restart.
type certReloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	modTime int64
}

func (r *certReloader) load() (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, err := os.Stat(r.certFile)
	if err != nil {
		return nil, fmt.Errorf("stat cert file: %w", err)
	}
	if r.cert != nil && info.ModTime().UnixNano() == r.modTime {
		return r.cert, nil
	}

	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load key pair: %w", err)
	}
	r.cert = &cert
	r.modTime = info.ModTime().UnixNano()
	return r.cert, nil
}
