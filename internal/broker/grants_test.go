package broker_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"github.com/ianeff/thump/internal/broker"
)

// beatUsers maps a beat's package directory to the nats.conf user its
// Deployment authenticates as — the cert SAN certificates.yaml mints and
// verify_and_map resolves. A beat missing from here publishes under no
// audited identity at all, so the population is authored, not discovered.
var beatUsers = map[string]string{
	"rattle": "rattle@thump.svc",
	"clank":  "clank@thump.svc",
	"hiss":   "hiss@thump.svc",
	"thump":  "thump@thump.svc",
}

// durableOwners maps each durable consumer name to the user whose process
// binds it. DurableFor names the consumer; it cannot name the identity, and
// three of the six names ("click", "clank-declines", "his-approvals") do not
// contain their owner's name — so ownership is authored here and an unmapped
// durable fails rather than passing silently.
var durableOwners = map[string]string{
	"clank":          "clank@thump.svc",
	"click":          "clank@thump.svc",
	"clank-declines": "clank@thump.svc",
	"hiss":           "hiss@thump.svc",
	"his-approvals":  "hiss@thump.svc",
	"thump":          "thump@thump.svc",
}

// publishedSubjects returns every subject literal pkgDir hands to a Publish
// call, read out of the AST rather than off a list somebody maintains — the
// list is the thing that failed. Non-literal subjects (subscriber.go's
// computed subject+".dlq") are invisible to this scan by construction, which
// is why the dead-letter half is derived separately.
func publishedSubjects(t *testing.T, pkgDir string) []string {
	t.Helper()

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}

	fset := token.NewFileSet()
	found := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(pkgDir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Publish" || len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			subj, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if strings.HasPrefix(subj, "thump.") {
				found[subj] = true
			}
			return true
		})
	}
	return slices.Sorted(maps.Keys(found))
}

func TestNATSConfig_GrantsEachBeatEverySubjectItsOwnCodePublishes(t *testing.T) {
	t.Parallel()
	// broker.Subjects is the set the broker provisions consumers for, not the
	// set the beats publish: thump.held, thump.orders and thump.declines have
	// no consumer, so every existing check looked straight past them while
	// they were published on every hold, every approved order, and every
	// non-approval. A missing grant here does not crash the beat — a
	// WALPublisher's local leg still succeeds, so the audit record exists and
	// the message never reaches the stream.
	users := parseNATSUsers(t, renderNATSConf(t))

	for pkg, user := range beatUsers {
		t.Run("nats.conf grants "+user+" every subject internal/"+pkg+" publishes", func(t *testing.T) {
			t.Parallel()

			granted := users[user].Publish
			var missing []string
			for _, subj := range publishedSubjects(t, "../"+pkg) {
				if !slices.Contains(granted, subj) {
					missing = append(missing, subj)
				}
			}
			if diff := cmp.Diff([]string(nil), missing); diff != "" {
				t.Error("internal/"+pkg+" publishes a subject nats.conf never grants "+user+" (-want +got)\n", diff)
			}
		})
	}
}

func TestNATSConfig_GrantsTheDeadLetterSubjectToWhicheverBeatConsumesTheOriginal(t *testing.T) {
	t.Parallel()
	// subscriber.go builds its dead-letter subject as subject+".dlq", so no
	// literal scan can see it, and both call sites discard the publish error —
	// a missing grant is completely silent on the one path that exists to
	// preserve a message which has already failed. The rule is derived, not
	// authored: whoever consumes S is the process that publishes S.dlq.
	users := parseNATSUsers(t, renderNATSConf(t))

	for _, subj := range broker.Subjects {
		durable := broker.DurableFor(subj)
		if durable == "" {
			continue
		}
		owner, ok := durableOwners[durable]
		if !ok {
			t.Errorf("durable %q (reading %s) has no owner in durableOwners — a consumer whose identity nobody named cannot be checked for its dead-letter grant", durable, subj)
			continue
		}
		if !slices.Contains(users[owner].Publish, subj+".dlq") {
			t.Errorf("%s consumes %s but may not publish %s.dlq — a message that exhausts its retry budget is dropped, not dead-lettered, and the failure is discarded", owner, subj, subj)
		}
	}
}

// grantExceptions are the publish grants deliberately held by an identity
// that is not a beat, each with the row that authorises it. Everything else
// in a user's allow list must be a subject that user's own package publishes.
var grantExceptions = map[string][]string{
	"calipers@thump.svc": {"thump.approvals", "thump.decisions"}, // D-9: the break-glass human path, attributed and audited
}

func TestNATSConfig_GrantsNoBeatASubjectItsOwnCodeNeverPublishes(t *testing.T) {
	t.Parallel()
	// The reverse of the grant check, and the half that catches an I-7 hole
	// rather than a silent drop: a user holding publish on a subject its beat
	// never writes is either dead config or a second producer for a boundary
	// object that has exactly one. $JS.* and .dlq grants are excluded — they
	// are checked against the consumer topology above, not against publish
	// sites.
	users := parseNATSUsers(t, renderNATSConf(t))

	for pkg, user := range beatUsers {
		t.Run("nats.conf grants "+user+" nothing internal/"+pkg+" does not publish", func(t *testing.T) {
			t.Parallel()

			published := publishedSubjects(t, "../"+pkg)
			var extra []string
			for _, subj := range users[user].Publish {
				if !strings.HasPrefix(subj, "thump.") || strings.HasSuffix(subj, ".dlq") {
					continue
				}
				if slices.Contains(published, subj) || slices.Contains(grantExceptions[user], subj) {
					continue
				}
				extra = append(extra, subj)
			}
			slices.Sort(extra)
			if diff := cmp.Diff([]string(nil), extra); diff != "" {
				t.Error("nats.conf grants "+user+" publish on a subject internal/"+pkg+" never writes (-want +got)\n", diff)
			}
		})
	}
}

func TestNATSConfig_GrantsTheJetStreamAPIsAndAckToEachDurablesOwner(t *testing.T) {
	t.Parallel()
	// Generalizes chart_test.go's old ACK-only check: a durable's owner needs
	// three grants to operate it — CONSUMER.INFO (EnsureTopology's bind
	// probe), CONSUMER.MSG.NEXT (Fetch), and $JS.ACK.<durable>.> (acking a
	// delivered message — a reply-subject publish that never goes through
	// $JS.API at all, see the nats.yaml file header). Missing INFO or
	// MSG.NEXT fails loudly at startup; missing ACK doesn't (R6e). And
	// checking only "some user holds it," as the old test did, would pass a
	// grant attributed to the wrong beat — the thump.declines shape, one
	// subject space over. This subsumes and replaces
	// TestNATSConfig_GrantsAckOnEveryDurableAConsumerBindsTo in chart_test.go.
	users := parseNATSUsers(t, renderNATSConf(t))
	for _, subj := range broker.Subjects {
		durable := broker.DurableFor(subj)
		if durable == "" {
			continue
		}
		owner, ok := durableOwners[durable]
		if !ok {
			t.Errorf("durable %q (reading %s) has no owner in durableOwners — cannot check its JetStream API/ACK grants", durable, subj)
			continue
		}
		for _, want := range []string{
			"$JS.API.CONSUMER.INFO.THUMP." + durable,
			"$JS.API.CONSUMER.MSG.NEXT.THUMP." + durable,
			"$JS.ACK.THUMP." + durable + ".>",
		} {
			if !slices.Contains(users[owner].Publish, want) {
				t.Errorf("%s owns durable %q (reading %s) but nats.conf does not grant it %q", owner, durable, subj, want)
			}
		}
	}
}

func TestNATSConfig_GrantsCalipersTheHarvestEphemeralConsumers(t *testing.T) {
	t.Parallel()
	// internal/harvest/nats.go's watchSubject opens a jetstream.OrderedConsumer
	// per watcher, on thump.outcomes and thump.proposals — same
	// randomly-named-ephemeral shape as hiss's rebuildHolds below, so the same
	// wildcard grants apply. Unlike rebuildHolds, an ordered consumer's
	// AckPolicy is AckNone (nats.go@v1.52.0 jetstream/ordered.go), so there is
	// no matching $JS.ACK grant to check here.
	users := parseNATSUsers(t, renderNATSConf(t))
	for _, want := range []string{
		"$JS.API.CONSUMER.CREATE.THUMP.*.thump.outcomes",
		"$JS.API.CONSUMER.CREATE.THUMP.*.thump.proposals",
		"$JS.API.CONSUMER.MSG.NEXT.THUMP.*",
	} {
		if !slices.Contains(users["calipers@thump.svc"].Publish, want) {
			t.Errorf("calipers@thump.svc does not hold %q — harvest's ephemeral consumers on thump.outcomes/thump.proposals cannot create/fetch without it", want)
		}
	}
}

// renderCertificates runs the real chart through `helm template --show-only`
// and returns every rendered Certificate's emailAddresses — the SAN
// verify_and_map resolves to a nats.conf user. A user with no matching
// Certificate has a permissions grant nobody can ever present a cert for.
func renderCertificates(t *testing.T) [][]string {
	t.Helper()

	out, err := exec.Command("helm", "template", "../../deploy/chart/thump", "--show-only", "templates/certificates.yaml").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}

	var all [][]string
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
			Spec struct {
				EmailAddresses []string `yaml:"emailAddresses"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode rendered certificates.yaml: %v", err)
		}
		if doc.Kind == "Certificate" {
			all = append(all, doc.Spec.EmailAddresses)
		}
	}
	return all
}

func TestCertificates_IssuesALeafForEveryNATSIdentity(t *testing.T) {
	t.Parallel()
	// nats.conf's verify_and_map maps a connecting client's cert by SAN
	// email to a user in authorization.users — a user with a real
	// permissions grant but no matching Certificate can never actually
	// connect as itself. This is exactly the calipers gap found live
	// 2026-08-06: the NATS grant landed in nats.yaml, but
	// certificates.yaml's identity loop was never extended to mint the
	// leaf, so nothing on disk could present that cert. Derived off
	// nats.conf's own user list rather than a fixed set, so the next
	// identity added to one file and forgotten in the other fails here
	// instead of live.
	certs := renderCertificates(t)
	users := parseNATSUsers(t, renderNATSConf(t))

	for user := range users {
		found := false
		for _, emails := range certs {
			if slices.Contains(emails, user) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("nats.conf grants %s permissions, but no rendered Certificate carries it as an emailAddress — nothing can connect as this identity", user)
		}
	}
}

func TestNATSConfig_GrantsHissTheRebuildHoldsEphemeralConsumer(t *testing.T) {
	t.Parallel()
	// rebuildHolds (internal/hiss/rebuild.go) mints a fresh, randomly-named
	// ephemeral consumer on thump.decisions every startup to replay
	// PendingHolds. The random name means these three grants can't be
	// derived from broker.Subjects × DurableFor like the durable grants
	// above — there's no fixed durable to name, only the wildcard token
	// nats.conf already carries. Authored rather than derived: a single case
	// doesn't earn its own derivation, but a silent absence here is the same
	// class of bug as everything else in this file.
	users := parseNATSUsers(t, renderNATSConf(t))
	for _, want := range []string{
		"$JS.API.CONSUMER.CREATE.THUMP.*.thump.decisions",
		"$JS.API.CONSUMER.MSG.NEXT.THUMP.*",
		"$JS.ACK.THUMP.*.>",
	} {
		if !slices.Contains(users["hiss@thump.svc"].Publish, want) {
			t.Errorf("hiss@thump.svc does not hold %q — rebuildHolds's ephemeral consumer replay of PendingHolds cannot create/fetch/ack without it", want)
		}
	}
}
