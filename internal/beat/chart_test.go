package beat_test

import (
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

// beats is every beat with its own binary under cmd/ and its own Deployment
// in the chart — the population this file's claim quantifies over.
var beats = []string{"clank", "hiss", "rattle", "thump"}

// TestChart_EveryBeatLinkingAClientGoClientHasCiliumApiserverEgress pins the
// two halves of apiserver access together: a beat that links a client-go
// client will dial kubernetes.default.svc, and on Cilium a plain
// NetworkPolicy's 0.0.0.0/0:443 rule does not reach it — the apiserver
// carries a reserved identity no CIDR rule matches. The failure is a dial
// timeout, not a refusal, so a beat missing its CiliumNetworkPolicy looks
// healthy while every call it makes stalls for 30 seconds and fails.
func TestChart_EveryBeatLinkingAClientGoClientHasCiliumApiserverEgress(t *testing.T) {
	t.Parallel()

	want := beatsLinkingClientGo(t)
	got := componentsWithCiliumApiserverEgress(t)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("a beat that dials the kube-apiserver has no CiliumNetworkPolicy granting it egress (-want +got)\n", diff)
	}
}

// beatsLinkingClientGo reports which beats' binaries actually link a
// client-go client, read from the build graph rather than a hand-kept list —
// the bug this guards against is a beat gaining client-go without anyone
// remembering the chart half.
func beatsLinkingClientGo(t *testing.T) []string {
	t.Helper()

	var linked []string
	for _, beat := range beats {
		out, err := runInRepoRoot(t, "go", "list", "-deps", "./cmd/"+beat)
		if err != nil {
			t.Fatalf("go list -deps ./cmd/%s: %v", beat, err)
		}
		for _, dep := range strings.Split(out, "\n") {
			if dep == "k8s.io/client-go/kubernetes" || dep == "k8s.io/client-go/dynamic" {
				linked = append(linked, beat)
				break
			}
		}
	}
	slices.Sort(linked)
	return linked
}

// componentsWithCiliumApiserverEgress renders the real chart with Cilium
// enabled and returns the components whose CiliumNetworkPolicy allows egress
// to the kube-apiserver entity — the same objects the cluster gets, not a
// copy a Go string could drift from.
func componentsWithCiliumApiserverEgress(t *testing.T) []string {
	t.Helper()

	out, err := runInRepoRoot(t, "helm", "template", "./deploy/chart/thump", "--set", "cilium.apiserverEgress=true")
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	var components []string
	dec := yaml.NewDecoder(strings.NewReader(out))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				EndpointSelector struct {
					MatchLabels map[string]string `yaml:"matchLabels"`
				} `yaml:"endpointSelector"`
				Egress []struct {
					ToEntities []string `yaml:"toEntities"`
				} `yaml:"egress"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered chart: %v", err)
		}
		if doc.Kind != "CiliumNetworkPolicy" {
			continue
		}
		for _, rule := range doc.Spec.Egress {
			if slices.Contains(rule.ToEntities, "kube-apiserver") {
				components = append(components, doc.Spec.EndpointSelector.MatchLabels["app.kubernetes.io/component"])
				break
			}
		}
	}
	slices.Sort(components)
	return components
}

func runInRepoRoot(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // G204: every name and arg here is a literal from this file, not user input
	cmd.Dir = "../.."
	out, err := cmd.Output()
	return string(out), err
}
