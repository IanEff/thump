package harvest_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/harvest"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/tlsx"
)

const scenarioYAMLTemplate = `
version: 1
rig: test-rig
scenarios:
  - name: main-smoke
    domain: test
    signalRef: fp-main-smoke
    fault:
      path: %[1]s
      apply: exec
    expects:
      failureClass: redundancy_degraded
      contractRef: restart-pod
      verdict: approved
    settleWindow: 5s
    restore:
      path: %[1]s
      apply: exec
`

// TestMain_RunsOneScenarioEndToEndAgainstARealBroker drives harvest.Main
// exactly the way calipers's dispatch table does — flags in, a real
// NATS/JetStream broker underneath — proving the whole pipeline (load
// table, connect, build the NATS watchers, run the scenario, print the
// Result) wires together, without needing a live cluster: the scenario's
// fault and restore both run the platform's "true" no-op, and the
// "outcome" is a plain publish this test makes itself rather than a real
// thump.
func TestMain_RunsOneScenarioEndToEndAgainstARealBroker(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no `true` binary on PATH:", err)
	}

	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	yaml := fmt.Sprintf(scenarioYAMLTemplate, truePath)
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	url := natstest.URL(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stand in for the topology Job: harvest.Main connects with plain
	// broker.Connect, the same as every beat, and expects the stream and
	// its durables to already exist rather than provisioning them itself.
	setupJS, closeSetup, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("ensure topology:", err)
	}

	// Published before Main even starts, standing in for clank proposing
	// well before thump's outcome settles — exactly the ordering
	// NATSSetWatcher's DeliverAllPolicy exists to handle, since Main's own
	// subscription can only start after this.
	setPub := publish.NewJetPublisher[proposal.Set](setupJS)
	set := proposal.Set{
		SignalRef: "fp-main-smoke",
		Proposals: []proposal.Candidate{{ContractRef: "restart-pod", Confidence: 0.7, ComputedConfidence: 0.65, ConfidenceCeilingBound: true}},
	}
	if err := setPub.Publish(ctx, "thump.proposals", set); err != nil {
		t.Fatal("publish set:", err)
	}
	closeSetup()

	// Retry the publish for a couple seconds: the scenario's own NATSWatcher
	// subscribes partway through Main's Run call, and DeliverNewPolicy only
	// sees messages published after that subscription exists. Publishing
	// once, early, would race losing the message entirely.
	go func() {
		js, closeNC, err := broker.Connect(ctx, url, tlsx.Config{}, broker.Hooks{})
		if err != nil {
			return
		}
		defer closeNC()
		pub := publish.NewJetPublisher[outcome.Outcome](js)
		o := outcome.Outcome{SignalRef: "fp-main-smoke", Result: outcome.ResultSuccess, ExecutedAt: time.Now()}
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = pub.Publish(ctx, "thump.outcomes", o)
			}
		}
	}()

	var stdout, stderr bytes.Buffer
	code := harvest.Main([]string{"--scenarios", path, "--nats-url", url}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stdout: %s) (stderr: %s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "main-smoke") {
		t.Errorf("want the scenario name in stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "OK") {
		t.Errorf("want an OK status line, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "confidence=0.700") {
		t.Errorf("want the pre-published Set's top confidence in stdout — DeliverAllPolicy should have found it despite subscribing after the publish, got %q", stdout.String())
	}
}
