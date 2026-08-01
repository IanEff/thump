package broker_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

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

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", pkgDir, err)
	}

	found := make(map[string]bool)
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
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
	"trim@thump.svc": {"thump.approvals", "thump.decisions"}, // D-9: the break-glass human path, attributed and audited
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
