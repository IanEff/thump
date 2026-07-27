package bootstrap_test

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"

	"github.com/ianeff/thump/internal/bootstrap"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/tlsxtest"
)

func TestMain_EnsuresTopologyOverTLSThenExitsZero(t *testing.T) {
	ca := tlsxtest.NewCA(t)
	serverCfg := ca.Leaf(t, "nats", tlsxtest.SANEmail("nats@thump.svc"), tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))
	clientCfg := ca.Leaf(t, "bootstrap", tlsxtest.SANEmail("bootstrap@thump.svc"))
	serverTLS, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatal("build server tls config:", err)
	}

	url := natstest.SecureURL(t, natstest.SecureOptions{
		TLSConfig: serverTLS,
		Users:     []*natssrv.User{{Username: "bootstrap@thump.svc"}}, // no Permissions: unrestricted, matching $JS.API.>
	})

	t.Setenv("NATS_URL", url)
	t.Setenv("TLS_CERT_FILE", clientCfg.CertFile)
	t.Setenv("TLS_KEY_FILE", clientCfg.KeyFile)
	t.Setenv("TLS_CA_FILE", clientCfg.CAFile)

	var stderr bytes.Buffer
	if code := bootstrap.Main(nil, &stderr); code != 0 {
		t.Fatalf("Main: want exit 0, got %d, stderr: %s", code, stderr.String())
	}

	// Prove the stream actually exists now, over a second plain connection —
	// Main's own success exit code isn't evidence on its own.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	js, closeNC, err := broker.Connect(ctx, url, clientCfg, broker.Hooks{})
	if err != nil {
		t.Fatal("reconnect to verify:", err)
	}
	defer closeNC()
	if _, err := js.Stream(ctx, broker.StreamName); err != nil {
		t.Fatal("Main exited 0 but the shared stream is missing:", err)
	}
}

func TestMain_ReturnsNonZeroWhenConfigIsMissing(t *testing.T) {
	for _, name := range []string{"NATS_URL", "TLS_CERT_FILE", "TLS_KEY_FILE", "TLS_CA_FILE"} {
		t.Setenv(name, "")
	}

	var stderr bytes.Buffer
	code := bootstrap.Main(nil, &stderr)
	if code == 0 {
		t.Fatal("Main: want a non-zero exit with no config set, got 0")
	}
	if !strings.Contains(stderr.String(), "NATS_URL") {
		t.Errorf("stderr must name the missing var, got %q", stderr.String())
	}
}

func TestMain_ReturnsNonZeroWhenNATSIsUnreachable(t *testing.T) {
	ca := tlsxtest.NewCA(t)
	clientCfg := ca.Leaf(t, "bootstrap", tlsxtest.SANEmail("bootstrap@thump.svc"))

	t.Setenv("NATS_URL", "tls://127.0.0.1:1") // closed port: fails fast, no real dial
	t.Setenv("TLS_CERT_FILE", clientCfg.CertFile)
	t.Setenv("TLS_KEY_FILE", clientCfg.KeyFile)
	t.Setenv("TLS_CA_FILE", clientCfg.CAFile)

	var stderr bytes.Buffer
	if code := bootstrap.Main(nil, &stderr); code == 0 {
		t.Fatal("Main: want a non-zero exit connecting to a closed port, got 0")
	}
}
