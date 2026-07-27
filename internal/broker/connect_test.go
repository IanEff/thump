package broker_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/tlsxtest"
)

func TestConnect_ReturnsAJetStreamContextAndAnIdempotentClose(t *testing.T) {
	t.Parallel()
	url := natstest.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	js, closeNC, err := broker.Connect(ctx, url, tlsx.Config{})
	if err != nil {
		t.Fatal("connect:", err)
	}
	defer closeNC()

	if js == nil {
		t.Fatal("Connect returned a nil JetStream context alongside a nil error")
	}

	closeNC() // idempotent: a second call must not panic or hang
}

func TestConnect_DoesNotEnsureTopology(t *testing.T) {
	t.Parallel()
	// The whole point of the Connect/ConnectAndEnsure split (R6c): an
	// ordinary beat's cert never needs $JS.API.> stream-create rights, only
	// the topology Job's does. If Connect quietly ensured topology anyway,
	// every beat would need that grant regardless of what ConnectAndEnsure
	// claims to be for.
	url := natstest.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	js, closeNC, err := broker.Connect(ctx, url, tlsx.Config{})
	if err != nil {
		t.Fatal("connect:", err)
	}
	defer closeNC()

	if _, err := js.Stream(ctx, broker.StreamName); err == nil {
		t.Fatal("Connect must not create the shared stream — that's ConnectAndEnsure's job alone")
	}
}

func TestConnectAndEnsure_CreatesTheStreamConnectAlonePreviouslyAssumed(t *testing.T) {
	t.Parallel()
	url := natstest.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	if _, err := js.Stream(ctx, broker.StreamName); err != nil {
		t.Fatal("ConnectAndEnsure didn't ensure the topology — stream missing:", err)
	}
}

func TestConnect_FailsOnABadURL(t *testing.T) {
	t.Parallel()
	if _, _, err := broker.Connect(context.Background(), "nats://127.0.0.1:1", tlsx.Config{}); err == nil {
		t.Fatal("expected an error connecting to a closed port")
	}
}

// mintServerAndClient hands back a server tlsx.Config (signed by ca) and a
// client leaf minted for cn under the same CA — the shared setup every
// handshake test below starts from before diverging into its one negative
// case. The server leaf carries an IP SAN for 127.0.0.1 because
// natstest.SecureURL binds there and has no DNS name to verify against.
func mintServerAndClient(t *testing.T, ca *tlsxtest.CA, cn string) (server, client tlsx.Config) {
	t.Helper()
	serverCfg := ca.Leaf(t, "nats", tlsxtest.SANEmail("nats@thump.svc"), tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))
	return serverCfg, ca.Leaf(t, cn, tlsxtest.SANEmail(cn+"@thump.svc"))
}

func TestConnect_HandshakesOverTLSWhenTheClientPresentsACertTheServerTrusts(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	serverCfg, clientCfg := mintServerAndClient(t, ca, "hiss")
	serverTLS, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatal("build server tls config:", err)
	}

	url := natstest.SecureURL(t, natstest.SecureOptions{
		TLSConfig: serverTLS,
		Users:     []*natssrv.User{{Username: "hiss@thump.svc"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	js, closeNC, err := broker.Connect(ctx, url, clientCfg)
	if err != nil {
		t.Fatal("connect over tls with a trusted cert must succeed:", err)
	}
	defer closeNC()

	if js == nil {
		t.Fatal("Connect returned a nil JetStream context alongside a nil error")
	}
}

func TestConnect_RefusesAServerCertSignedByAnUntrustedCA(t *testing.T) {
	t.Parallel()
	serverCA := tlsxtest.NewCA(t)
	clientCA := tlsxtest.NewCA(t) // a different root: the client's CAFile never signed the server's leaf
	serverCfg, _ := mintServerAndClient(t, serverCA, "hiss")
	_, clientCfg := mintServerAndClient(t, clientCA, "hiss")
	serverTLS, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatal("build server tls config:", err)
	}

	url := natstest.SecureURL(t, natstest.SecureOptions{
		TLSConfig: serverTLS,
		Users:     []*natssrv.User{{Username: "hiss@thump.svc"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := broker.Connect(ctx, url, clientCfg); err == nil {
		t.Fatal("a server cert signed by a CA the client doesn't trust must be refused, got a live connection")
	}
}

func TestConnect_ServerRefusesAClientCertSignedByASecondCA(t *testing.T) {
	t.Parallel()
	serverCA := tlsxtest.NewCA(t)
	imposterCA := tlsxtest.NewCA(t) // trusted by nobody: not in the server's ClientCAs pool
	serverCfg, _ := mintServerAndClient(t, serverCA, "hiss")
	_, imposterCfg := mintServerAndClient(t, imposterCA, "hiss")
	serverTLS, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatal("build server tls config:", err)
	}
	// The imposter's client dials serverCA's own CA file so the *server's*
	// cert verifies fine — the point under test is the server refusing the
	// imposter's client cert, not the client refusing the server's.
	imposterCfg.CAFile = serverCfg.CAFile

	url := natstest.SecureURL(t, natstest.SecureOptions{
		TLSConfig: serverTLS,
		Users:     []*natssrv.User{{Username: "hiss@thump.svc"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := broker.Connect(ctx, url, imposterCfg); err == nil {
		t.Fatal("a client cert from a CA the server never verified must be refused, got a live connection")
	}
}

func TestConnect_ServerRefusesAClientPresentingNoCertificate(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	serverCfg, _ := mintServerAndClient(t, ca, "hiss")
	serverTLS, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatal("build server tls config:", err)
	}

	url := natstest.SecureURL(t, natstest.SecureOptions{
		TLSConfig: serverTLS,
		Users:     []*natssrv.User{{Username: "hiss@thump.svc"}},
	})

	// CAFile only, no CertFile/KeyFile — tlsx.Client's one-way TLS shape.
	noCertCfg := tlsx.Config{CAFile: serverCfg.CAFile}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := broker.Connect(ctx, url, noCertCfg); err == nil {
		t.Fatal("an mTLS server (RequireAndVerifyClientCert) must refuse a client presenting no certificate, got a live connection")
	}
}

// TestBrokerAuthz_OnlyHissMayPublishThumpDecisions proves the shape R6a's
// nats.conf authorization table takes actually enforces I-7 (hiss is the
// only producer of a verdict) — bypassing broker.Connect to dial nats.go
// directly, because catching the async permissions-violation error nats-
// server sends after a denied publish needs an ErrorHandler broker.Connect
// doesn't (and shouldn't) expose.
func TestBrokerAuthz_OnlyHissMayPublishThumpDecisions(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	serverCfg, _ := mintServerAndClient(t, ca, "hiss")
	serverTLS, err := tlsx.Server(serverCfg)
	if err != nil {
		t.Fatal("build server tls config:", err)
	}

	url := natstest.SecureURL(t, natstest.SecureOptions{
		TLSConfig: serverTLS,
		Users: []*natssrv.User{
			{Username: "hiss@thump.svc", Permissions: &natssrv.Permissions{
				Publish: &natssrv.SubjectPermission{Allow: []string{"thump.decisions"}},
			}},
			{Username: "clank@thump.svc", Permissions: &natssrv.Permissions{
				Publish: &natssrv.SubjectPermission{Allow: []string{"thump.proposals"}},
			}},
		},
	})

	_, hissCfg := mintServerAndClient(t, ca, "hiss")
	_, clankCfg := mintServerAndClient(t, ca, "clank")

	publishAndAwaitResult := func(t *testing.T, cfg tlsx.Config) error {
		t.Helper()
		tc, err := tlsx.Client(cfg)
		if err != nil {
			t.Fatal("build client tls config:", err)
		}
		errCh := make(chan error, 1)
		nc, err := nats.Connect(url, nats.Secure(tc), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			errCh <- err
		}))
		if err != nil {
			t.Fatal("connect:", err)
		}
		defer nc.Close()

		if err := nc.Publish("thump.decisions", []byte("{}")); err != nil {
			t.Fatal("publish:", err)
		}
		if err := nc.Flush(); err != nil {
			t.Fatal("flush:", err)
		}
		select {
		case err := <-errCh:
			return err
		case <-time.After(time.Second):
			return nil
		}
	}

	if err := publishAndAwaitResult(t, hissCfg); err != nil {
		t.Error("hiss@thump.svc must be able to publish thump.decisions — it's the sole producer of a verdict (I-7)", err)
	}
	clankErr := publishAndAwaitResult(t, clankCfg)
	if clankErr == nil {
		t.Fatal("clank@thump.svc published thump.decisions without error — I-3, I-7 and I-10 are all undone if a second identity can author a verdict")
	}
	if !errors.Is(clankErr, nats.ErrPermissionViolation) {
		t.Error("expected a permissions violation refusing clank's publish", clankErr)
	}
}
