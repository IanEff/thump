package beat_test

import (
	"errors"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dialedEndpoint is one literal env value that resolves to a host:port a
// beat actually dials — never a valueFrom (S3_ENDPOINT, ANTHROPIC_API_KEY
// are secretKeyRefs and structurally invisible here, which is the read-back
// finding in code form, not a name-based exception list).
type dialedEndpoint struct {
	envVar, host, port string
}

// TestChart_EveryDialedEndpointHasMatchingEgress derives every outbound
// endpoint a beat's rendered Deployment carries as a literal env value and
// requires the component's own NetworkPolicy/CiliumNetworkPolicy to open
// that port. A plain NetworkPolicy has no hostname primitive
// (netpol-clank.yaml's own comment on this), so "admits" means "some
// egress rule opens this port" — the same resolution the policies
// themselves carry, not a stronger claim than the config supports.
func TestChart_EveryDialedEndpointHasMatchingEgress(t *testing.T) {
	t.Parallel()

	out, err := runInRepoRoot(t, "helm", "template", "./deploy/chart/thump",
		"--set", "tracing.endpoint=https://tempo.tracing.svc.cluster.local:4317")
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	dialed := dialedEndpoints(t, out)
	open := egressPorts(t, out)

	for component, endpoints := range dialed {
		for _, ep := range endpoints {
			if !slices.Contains(open[component], ep.port) {
				t.Errorf("%s's %s dials %s:%s but no NetworkPolicy/CiliumNetworkPolicy egress rule for %s opens port %s",
					component, ep.envVar, ep.host, ep.port, component, ep.port)
			}
		}
	}
}

func dialedEndpoints(t *testing.T, rendered string) map[string][]dialedEndpoint {
	t.Helper()
	found := make(map[string][]dialedEndpoint)

	dec := yaml.NewDecoder(strings.NewReader(rendered))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Template struct {
					Metadata struct {
						Labels map[string]string `yaml:"labels"`
					} `yaml:"metadata"`
					Spec struct {
						Containers []struct {
							Env []struct {
								Name  string `yaml:"name"`
								Value string `yaml:"value"`
							} `yaml:"env"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered chart: %v", err)
		}
		if doc.Kind != "Deployment" {
			continue
		}
		component := doc.Spec.Template.Metadata.Labels["app.kubernetes.io/component"]
		for _, c := range doc.Spec.Template.Spec.Containers {
			for _, e := range c.Env {
				host, port, ok := hostPort(e.Value)
				if !ok {
					continue
				}
				found[component] = append(found[component], dialedEndpoint{e.Name, host, port})
			}
		}
	}
	return found
}

// hostPort reports the host:port a literal env value dials, or false —
// filtering out bind addresses (METRICS_ADDR=":9090" has an empty host: a
// listen port, not an egress target) and everything that isn't
// scheme://host:port or bare host:port shaped at all.
func hostPort(v string) (host, port string, ok bool) {
	rest := v
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	h, p, err := net.SplitHostPort(rest)
	if err != nil || h == "" {
		return "", "", false
	}
	if _, err := strconv.Atoi(p); err != nil {
		return "", "", false
	}
	return h, p, true
}

// egressPorts reports every port some NetworkPolicy or
// CiliumNetworkPolicy egress rule opens for a component, read off the same
// rendered manifest.
func egressPorts(t *testing.T, rendered string) map[string][]string {
	t.Helper()
	open := make(map[string][]string)

	dec := yaml.NewDecoder(strings.NewReader(rendered))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				PodSelector struct {
					MatchLabels map[string]string `yaml:"matchLabels"`
				} `yaml:"podSelector"`
				EndpointSelector struct {
					MatchLabels map[string]string `yaml:"matchLabels"`
				} `yaml:"endpointSelector"`
				Egress []struct {
					Ports []struct {
						Port int `yaml:"port"`
					} `yaml:"ports"`
				} `yaml:"egress"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered chart: %v", err)
		}
		if doc.Kind != "NetworkPolicy" && doc.Kind != "CiliumNetworkPolicy" {
			continue
		}
		component := doc.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"]
		if component == "" {
			component = doc.Spec.EndpointSelector.MatchLabels["app.kubernetes.io/component"]
		}
		for _, rule := range doc.Spec.Egress {
			for _, p := range rule.Ports {
				open[component] = append(open[component], strconv.Itoa(p.Port))
			}
		}
	}
	return open
}
