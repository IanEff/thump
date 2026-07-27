package broker_test

import (
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/conf"
	"gopkg.in/yaml.v3"

	"github.com/ianeff/thump/internal/broker"
)

// renderNATSConf runs the real chart through `helm template --show-only`
// (already a task ci dependency via chart-lint) and returns the thump-nats
// ConfigMap's nats.conf text — the same bytes NATS boots from in the
// cluster, not a copy a Go string could silently drift from.
func renderNATSConf(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("helm", "template", "../../deploy/chart/thump", "--show-only", "templates/nats.yaml").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var doc struct {
			Kind string            `yaml:"kind"`
			Data map[string]string `yaml:"data"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered nats.yaml: %v", err)
		}
		if doc.Kind == "ConfigMap" {
			return doc.Data["nats.conf"]
		}
	}
	t.Fatal("rendered nats.yaml has no ConfigMap")
	return ""
}

// natsUser is the one thing these tests need out of nats.conf's parsed
// authorization block — everything else (subscribe grants, TLS block) isn't
// what the charter invariants below are checking.
type natsUser struct {
	Publish []string
}

// parseNATSUsers parses natsConf with nats-server's own conf package — the
// same parser the real broker uses to load this file — rather than
// string-matching the raw text, so a reformatted-but-equivalent nats.conf
// never breaks these tests for the wrong reason.
func parseNATSUsers(t *testing.T, natsConf string) map[string]natsUser {
	t.Helper()

	m, err := conf.Parse(natsConf)
	if err != nil {
		t.Fatalf("parse nats.conf: %v", err)
	}
	authz, ok := m["authorization"].(map[string]any)
	if !ok {
		t.Fatal("nats.conf has no authorization block")
	}
	rawUsers, ok := authz["users"].([]any)
	if !ok {
		t.Fatal("authorization block has no users array")
	}

	users := make(map[string]natsUser, len(rawUsers))
	for _, ru := range rawUsers {
		u, ok := ru.(map[string]any)
		if !ok {
			continue
		}
		name, _ := u["user"].(string)

		var publish []string
		if perms, ok := u["permissions"].(map[string]any); ok {
			if pub, ok := perms["publish"].(map[string]any); ok {
				if allow, ok := pub["allow"].([]any); ok {
					for _, a := range allow {
						if s, ok := a.(string); ok {
							publish = append(publish, s)
						}
					}
				}
			}
		}
		users[name] = natsUser{Publish: publish}
	}
	return users
}

func TestNATSConfig_GrantsAPublisherToEverySubjectTheBrokerProvisions(t *testing.T) {
	t.Parallel()
	// helm template is already a CI dependency (Taskfile chart-lint), so
	// rendering here costs nothing new. The claim: a subject that exists in
	// Go but has no publisher in nats.conf is a beat that fails its first
	// publish in the cluster and passes every test on a laptop.
	nc := renderNATSConf(t)
	for _, subject := range broker.Subjects {
		if !strings.Contains(nc, subject) {
			t.Errorf("nats.conf grants nobody publish on %q — internal/broker provisions a consumer for it, so somebody has to be able to write it", subject)
		}
	}
}

func TestNATSConfig_GrantsNobodyButHissPublishOnDecisions(t *testing.T) {
	t.Parallel()
	// I-7: hiss is the only producer of a verdict. Until R6 that was a
	// convention; here it is the broker's config. A second user with publish
	// on thump.decisions is I-3, I-7 and I-10 undone at once, and it would
	// otherwise be a two-line diff nobody reads twice.
	users := parseNATSUsers(t, renderNATSConf(t))
	for name, u := range users {
		if slices.Contains(u.Publish, "thump.decisions") && name != "hiss@thump.svc" {
			t.Errorf("%s may publish thump.decisions — only hiss may author a verdict (I-7)", name)
		}
	}
}
