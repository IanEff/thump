package httpx_test

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/tlsxtest"
)

// startTLSBackend serves one 200 OK over serverCfg's leaf and shuts itself
// down on test cleanup — standing in for a Prometheus or Loki backend once
// either starts serving TLS, without needing a cluster to prove it. ErrorLog
// is silenced because TestClient_RefusesAPeerFromAnUntrustedCA deliberately
// drives a handshake failure, and net/http's default logger would otherwise
// print that expected rejection as if the suite had a real error.
func startTLSBackend(t *testing.T, serverCfg tlsx.Config) (addr string) {
	t.Helper()

	tc, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &http.Server{
		TLSConfig:         tc,
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ErrorLog:          log.New(io.Discard, "", 0),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	return ln.Addr().String()
}

// TestClient_DialsAPeerVerifiedAgainstTheGivenCA pins R7c: the tlsCfg param
// Client grew isn't just plumbed through unused — it's what lets a call
// reach a peer whose leaf chains to a private CA no public root pool would
// ever trust, which is exactly what dialing PROM_URL/LOKI_URL over https
// needs the day either backend starts serving TLS.
func TestClient_DialsAPeerVerifiedAgainstTheGivenCA(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverLeaf := ca.Leaf(t, "backend", tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))
	clientLeaf := ca.Leaf(t, "beat")

	addr := startTLSBackend(t, serverLeaf)

	clientTC, err := tlsx.Client(clientLeaf)
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}

	resp, err := httpx.Client(httpx.DefaultBackendTimeout, clientTC).Get("https://" + addr)
	if err != nil {
		t.Fatalf("GET a peer certified by the given CA: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestClient_RefusesAPeerFromAnUntrustedCA pins the other half: RootCAs is a
// real check on the transport Client builds, not a non-nil TLSClientConfig
// that waves every handshake through once it's set.
func TestClient_RefusesAPeerFromAnUntrustedCA(t *testing.T) {
	t.Parallel()

	serverCA := tlsxtest.NewCA(t)
	otherCA := tlsxtest.NewCA(t)
	serverLeaf := serverCA.Leaf(t, "backend", tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))

	addr := startTLSBackend(t, serverLeaf)

	clientTC, err := tlsx.Client(tlsx.Config{CAFile: otherCA.Leaf(t, "decoy").CAFile})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}

	_, err = httpx.Client(httpx.DefaultBackendTimeout, clientTC).Get("https://" + addr)
	if err == nil {
		t.Error("GET a peer certified by an untrusted CA succeeded, want a TLS verification error")
	}
}
