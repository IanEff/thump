package tlsx_test

import (
	"crypto/tls"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/tlsxtest"
)

// handshake completes a TLS handshake between serverCfg and clientCfg over a
// loopback TCP connection, returning both connections — still open — and
// each side's Handshake error. It uses a real socket rather than net.Pipe:
// net.Pipe's Write blocks until the peer's Read is ready for it, so a side
// that fails first and stops reading leaves the other blocked forever
// trying to send its closing TLS alert. Callers that only care whether the
// handshake succeeded should use handshakeErrs instead; this one exists for
// the rotation test, which needs the client's ConnectionState afterward.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) (server, client *tls.Conn, serverErr, clientErr error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	var wg sync.WaitGroup
	wg.Go(func() {
		rawServer, acceptErr := ln.Accept()
		if acceptErr != nil {
			serverErr = acceptErr
			return
		}
		server = tls.Server(rawServer, serverCfg)
		serverErr = server.Handshake()
	})
	wg.Go(func() {
		rawClient, dialErr := net.Dial("tcp", ln.Addr().String())
		if dialErr != nil {
			clientErr = dialErr
			return
		}
		client = tls.Client(rawClient, clientCfg)
		clientErr = client.Handshake()
	})
	wg.Wait()

	return server, client, serverErr, clientErr
}

// handshakeErrs is handshake for callers that only need to know whether each
// side accepted the other; it closes both connections before returning.
func handshakeErrs(t *testing.T, serverCfg, clientCfg *tls.Config) (serverErr, clientErr error) {
	t.Helper()

	server, client, serverErr, clientErr := handshake(t, serverCfg, clientCfg)
	if server != nil {
		_ = server.Close()
	}
	if client != nil {
		_ = client.Close()
	}
	return serverErr, clientErr
}

func TestClient_ServerLeafFromDifferentCA_HandshakeRefused(t *testing.T) {
	t.Parallel()

	serverCA := tlsxtest.NewCA(t)
	otherCA := tlsxtest.NewCA(t)
	serverLeaf := serverCA.Leaf(t, "server")
	untrustedCAFile := otherCA.Leaf(t, "decoy").CAFile

	serverCfg, err := tlsx.Server(tlsx.Config{
		CertFile: serverLeaf.CertFile,
		KeyFile:  serverLeaf.KeyFile,
		CAFile:   serverLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	// The client only needs to verify the server here, so it presents no
	// certificate of its own — the server side's ClientAuth is exercised by
	// TestServer_ClientLeafFromDifferentCA_HandshakeRefused instead.
	clientCfg, err := tlsx.Client(tlsx.Config{CAFile: untrustedCAFile})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientCfg.ServerName = "server"

	_, clientErr := handshakeErrs(t, serverCfg, clientCfg)
	if clientErr == nil {
		t.Error("client verified a server certificate signed by a CA it wasn't given — RootCAs isn't doing its job")
	}
}

func TestServer_ClientLeafFromDifferentCA_HandshakeRefused(t *testing.T) {
	t.Parallel()

	serverCA := tlsxtest.NewCA(t)
	otherCA := tlsxtest.NewCA(t)
	serverLeaf := serverCA.Leaf(t, "server")
	badClientLeaf := otherCA.Leaf(t, "client")

	serverCfg, err := tlsx.Server(tlsx.Config{
		CertFile: serverLeaf.CertFile,
		KeyFile:  serverLeaf.KeyFile,
		CAFile:   serverLeaf.CAFile, // trusts serverCA only
	})
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	clientCfg, err := tlsx.Client(tlsx.Config{
		CertFile: badClientLeaf.CertFile,
		KeyFile:  badClientLeaf.KeyFile,
		CAFile:   serverLeaf.CAFile, // trusts serverCA, so it gets far enough to present its own cert
	})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientCfg.ServerName = "server"

	serverErr, _ := handshakeErrs(t, serverCfg, clientCfg)
	if serverErr == nil {
		t.Error("server accepted a client certificate signed by a CA outside ClientCAs — RequireAndVerifyClientCert isn't verifying")
	}
}

func TestClient_ExpiredServerLeaf_HandshakeRefused(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	expiredServerLeaf := ca.Leaf(t, "server", tlsxtest.Expired())
	clientLeaf := ca.Leaf(t, "client")

	serverCfg, err := tlsx.Server(tlsx.Config{
		CertFile: expiredServerLeaf.CertFile,
		KeyFile:  expiredServerLeaf.KeyFile,
		CAFile:   expiredServerLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	clientCfg, err := tlsx.Client(tlsx.Config{
		CertFile: clientLeaf.CertFile,
		KeyFile:  clientLeaf.KeyFile,
		CAFile:   clientLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientCfg.ServerName = "server"

	_, clientErr := handshakeErrs(t, serverCfg, clientCfg)
	if clientErr == nil {
		t.Error("client accepted a server certificate past its NotAfter")
	}
}

func TestClient_ServerNameMismatch_HandshakeRefused(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverLeaf := ca.Leaf(t, "server") // DNSNames: ["server"]
	clientLeaf := ca.Leaf(t, "client")

	serverCfg, err := tlsx.Server(tlsx.Config{
		CertFile: serverLeaf.CertFile,
		KeyFile:  serverLeaf.KeyFile,
		CAFile:   serverLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	clientCfg, err := tlsx.Client(tlsx.Config{
		CertFile: clientLeaf.CertFile,
		KeyFile:  clientLeaf.KeyFile,
		CAFile:   clientLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientCfg.ServerName = "not-server" // the leaf's only DNS SAN is "server"

	_, clientErr := handshakeErrs(t, serverCfg, clientCfg)
	if clientErr == nil {
		t.Error("client accepted a server certificate whose SAN doesn't match the dialed ServerName")
	}
}

func TestClient_GarbageCAFile_ConstructionErrors(t *testing.T) {
	t.Parallel()

	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write garbage CA file: %v", err)
	}

	_, err := tlsx.Client(tlsx.Config{CAFile: caFile})
	if err == nil {
		t.Fatal("Client returned a config from a garbage CA file instead of erroring — a caller would get an empty pool or the system roots, silently")
	}
}

func TestServer_GarbageCAFile_ConstructionErrors(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverLeaf := ca.Leaf(t, "server")
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write garbage CA file: %v", err)
	}

	_, err := tlsx.Server(tlsx.Config{
		CertFile: serverLeaf.CertFile,
		KeyFile:  serverLeaf.KeyFile,
		CAFile:   caFile,
	})
	if err == nil {
		t.Fatal("Server returned a config from a garbage CA file instead of erroring")
	}
}

func TestServer_ClientPresentsNoCertificate_HandshakeRefused(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverLeaf := ca.Leaf(t, "server")

	serverCfg, err := tlsx.Server(tlsx.Config{
		CertFile: serverLeaf.CertFile,
		KeyFile:  serverLeaf.KeyFile,
		CAFile:   serverLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	// No CertFile/KeyFile: the client verifies the server but presents
	// nothing of its own.
	clientCfg, err := tlsx.Client(tlsx.Config{CAFile: serverLeaf.CAFile})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientCfg.ServerName = "server"

	serverErr, _ := handshakeErrs(t, serverCfg, clientCfg)
	if serverErr == nil {
		t.Error("server accepted a handshake with no client certificate — ClientAuth isn't RequireAndVerifyClientCert")
	}
}

func TestServer_RotatedKeypair_PickedUpWithoutRestart(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverLeaf := ca.Leaf(t, "server")
	clientLeaf := ca.Leaf(t, "client")

	serverCfg, err := tlsx.Server(tlsx.Config{
		CertFile: serverLeaf.CertFile,
		KeyFile:  serverLeaf.KeyFile,
		CAFile:   serverLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	clientCfg, err := tlsx.Client(tlsx.Config{
		CertFile: clientLeaf.CertFile,
		KeyFile:  clientLeaf.KeyFile,
		CAFile:   clientLeaf.CAFile,
	})
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientCfg.ServerName = "server"

	before := serverSerial(t, serverCfg, clientCfg)

	ca.Rotate(t, serverLeaf, "server")

	after := serverSerial(t, serverCfg, clientCfg)

	if before.Cmp(after) == 0 {
		t.Error("server presented the same certificate serial after its files were rotated — the loader isn't rereading them")
	}
}

// serverSerial completes a handshake and returns the serial number of the
// certificate the server presented, from the client's verified chain.
func serverSerial(t *testing.T, serverCfg, clientCfg *tls.Config) *big.Int {
	t.Helper()

	server, client, serverErr, clientErr := handshake(t, serverCfg, clientCfg)
	if server != nil {
		defer func() { _ = server.Close() }()
	}
	if client != nil {
		defer func() { _ = client.Close() }()
	}
	if serverErr != nil {
		t.Fatalf("server handshake: %v", serverErr)
	}
	if clientErr != nil {
		t.Fatalf("client handshake: %v", clientErr)
	}

	state := client.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		t.Fatal("client connection state has no peer certificates")
	}
	return state.PeerCertificates[0].SerialNumber
}
