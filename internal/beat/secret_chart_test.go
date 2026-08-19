package beat_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

// TestChart_SecretsOffByDefault verifies that neither thump-seal nor nats-js-key
// Secrets are rendered by default, preventing ephemeral in-namespace key generation (D-31).
func TestChart_SecretsOffByDefault(t *testing.T) {
	t.Parallel()

	out, err := runInRepoRoot(t, "helm", "template", "./deploy/chart/thump")
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	var secretNames []string
	dec := yaml.NewDecoder(strings.NewReader(out))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered chart: %v", err)
		}
		if doc.Kind == "Secret" {
			secretNames = append(secretNames, doc.Metadata.Name)
		}
	}

	var want []string
	if diff := cmp.Diff(want, secretNames); diff != "" {
		t.Errorf("chart renders unexpected secrets by default (-want +got):\n%s", diff)
	}
}

// TestChart_OptInSecretsRenderWhenExplicitlyEnabled verifies that dev overrides
// (--set seal.create=true --set seal.key=...) render the requested secret.
func TestChart_OptInSecretsRenderWhenExplicitlyEnabled(t *testing.T) {
	t.Parallel()

	//nolint:gosec // G101: synthetic test key fixture, not real credentials
	testCases := map[string]struct {
		args       []string
		wantSecret string
		wantKey    string
		wantVal    string
	}{
		"seal.create=true renders thump-seal with provided key": {
			args:       []string{"--set", "seal.create=true", "--set", "seal.key=dGVzdC1zZWFsLWtleQ=="},
			wantSecret: "thump-seal",
			wantKey:    "key",
			wantVal:    "dGVzdC1zZWFsLWtleQ==",
		},
		"nats.jetstream.create=true renders nats-js-key with provided key": {
			args:       []string{"--set", "nats.jetstream.create=true", "--set", "nats.jetstream.key=dGVzdC1qcy1rZXk="},
			wantSecret: "nats-js-key",
			wantKey:    "key",
			wantVal:    "dGVzdC1qcy1rZXk=",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"template", "./deploy/chart/thump"}, tc.args...)
			out, err := runInRepoRoot(t, "helm", args...)
			if err != nil {
				t.Fatalf("helm template: %v", err)
			}

			found := false
			dec := yaml.NewDecoder(strings.NewReader(out))
			for {
				var doc struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Name string `yaml:"name"`
					} `yaml:"metadata"`
					StringData map[string]string `yaml:"stringData"`
				}
				if err := dec.Decode(&doc); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					t.Fatalf("decode rendered chart: %v", err)
				}
				if doc.Kind == "Secret" && doc.Metadata.Name == tc.wantSecret {
					found = true
					if got := doc.StringData[tc.wantKey]; got != tc.wantVal {
						t.Errorf("secret %s key %s: want %q, got %q", tc.wantSecret, tc.wantKey, tc.wantVal, got)
					}
				}
			}
			if !found {
				t.Errorf("secret %s was not rendered", tc.wantSecret)
			}
		})
	}
}
