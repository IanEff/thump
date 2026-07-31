package hiss_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/internal/hiss"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// approvalRequestGVR mirrors the production constant privately — the wire
// contract (group/version/resource), not an implementation detail, so
// pinning it as a literal here is the same call as hardcoding "thump.decisions"
// across the rest of hiss's tests.
var approvalRequestGVR = schema.GroupVersionResource{Group: "thump.dev", Version: "v1alpha1", Resource: "approvalrequests"}

type fakeApprovalPub struct{ published []approval.Approval }

func (f *fakeApprovalPub) Publish(_ context.Context, _ string, a approval.Approval) error {
	f.published = append(f.published, a)
	return nil
}

// fakeDyn builds a dynamic fake client seeded with objs, using the custom
// list-kind constructor: ApprovalRequest has no generated Go type or scheme
// registration (it's read and written purely as unstructured content), and
// the plain NewSimpleDynamicClient can't infer a List kind for a type the
// scheme doesn't know — same shape internal/actuate/kube_test.go uses for
// registered types, one step further for an unregistered one.
func fakeDyn(t *testing.T, objs ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{approvalRequestGVR: "ApprovalRequestList"}
	items := make([]runtime.Object, len(objs))
	for i, o := range objs {
		items[i] = o
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, items...)
}

func approvalRequestObj(name string, spec, status map[string]interface{}, annotations map[string]string) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "thump.dev/v1alpha1",
		"kind":       "ApprovalRequest",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "thump",
		},
		"spec": spec,
	}
	if status != nil {
		obj["status"] = status
	}
	u := &unstructured.Unstructured{Object: obj}
	if annotations != nil {
		u.SetAnnotations(annotations)
	}
	return u
}

func getStatus(t *testing.T, dyn *dynamicfake.FakeDynamicClient, name string) map[string]interface{} {
	t.Helper()
	got, err := dyn.Resource(approvalRequestGVR).Namespace("thump").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	status, _, _ := unstructured.NestedMap(got.Object, "status")
	return status
}

func TestNewApprovalRequestController_FailsClosedOutsideACluster(t *testing.T) {
	t.Parallel()
	// Not a behavior pin — there's no fake for rest.InClusterConfig — just
	// proof this fails loud rather than panicking or silently building a
	// controller pointed at nothing when hiss isn't actually running as a pod.
	if _, err := hiss.NewApprovalRequestController(nil); err == nil {
		t.Fatal("want an error building the in-cluster config outside a real cluster")
	}
}

func TestApprovalRequestName_IsStableAndDNS1123Safe(t *testing.T) {
	t.Parallel()
	// rattle's fingerprints are "kind:object" (see internal/rattle/reconcile.go's
	// fingerprint) — colons, and whatever characters AffectedObject returns,
	// neither of which Kubernetes object names permit.
	fp := "deployment:otel-cart"

	got := hiss.ApprovalRequestNameForTest(fp)
	again := hiss.ApprovalRequestNameForTest(fp)
	if diff := cmp.Diff(got, again); diff != "" {
		t.Error("the same fingerprint must map to the same object name every time (-first +second)", diff)
	}

	dns1123 := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	if !dns1123.MatchString(got) || len(got) > 253 {
		t.Errorf("approvalRequestName(%q) = %q, not a valid Kubernetes object name", fp, got)
	}

	if other := hiss.ApprovalRequestNameForTest("deployment:otel-frontend"); other == got {
		t.Error("two different fingerprints must not collide onto the same object name")
	}
}

func TestApprovalRequestController_Create_WritesTheSpecFromAHeldDecision(t *testing.T) {
	t.Parallel()
	dyn := fakeDyn(t)
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump"}
	held := heldGoverned(t)

	if err := c.Create(context.Background(), held); err != nil {
		t.Fatal(err)
	}

	name := hiss.ApprovalRequestNameForTest(held.Decision.SignalRef)
	got, err := dyn.Resource(approvalRequestGVR).Namespace("thump").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal("Create must write a CR the same fingerprint's name resolves to:", err)
	}

	wantSpec := hiss.NewApprovalRequestSpecForTest(held)
	signalRef, _, _ := unstructured.NestedString(got.Object, "spec", "signalRef")
	action, _, _ := unstructured.NestedString(got.Object, "spec", "action")
	band, _, _ := unstructured.NestedString(got.Object, "spec", "band")
	if diff := cmp.Diff(wantSpec.SignalRef, signalRef); diff != "" {
		t.Error("wrong spec.signalRef (-want +got)", diff)
	}
	if diff := cmp.Diff(wantSpec.Action, action); diff != "" {
		t.Error("wrong spec.action (-want +got)", diff)
	}
	if diff := cmp.Diff(wantSpec.Band, band); diff != "" {
		t.Error("wrong spec.band (-want +got)", diff)
	}
}

func TestApprovalRequestController_Create_ExistingCRIsNotAnError(t *testing.T) {
	t.Parallel()
	held := heldGoverned(t)
	name := hiss.ApprovalRequestNameForTest(held.Decision.SignalRef)
	dyn := fakeDyn(t, approvalRequestObj(name, map[string]interface{}{"signalRef": held.Decision.SignalRef, "action": "x", "band": "y"}, nil, nil))
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump"}

	// A redelivered hold finding its CR already there is a no-op, not a
	// failure — Holds.Record's doc comment already guarantees clank's
	// dedupe ledger keeps a fingerprint from firing twice in practice, so
	// this defends the boundary rather than a path hiss expects to hit.
	if err := c.Create(context.Background(), held); err != nil {
		t.Error("AlreadyExists must not surface as an error:", err)
	}
}

func TestApprovalRequestController_Reconcile_ApprovePublishesAnAckAndMarksProcessed(t *testing.T) {
	t.Parallel()
	obj := approvalRequestObj("ar-1",
		map[string]interface{}{"signalRef": "fp-1", "decision": "approve"},
		nil, map[string]string{"thump.dev/approved-by": "alice"})
	dyn := fakeDyn(t, obj)
	approvePub := &fakeApprovalPub{}
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: approvePub, Now: frozenNow}

	if err := c.ReconcileForTest(context.Background(), obj); err != nil {
		t.Fatal(err)
	}

	want := []approval.Approval{{SignalRef: "fp-1", Approver: "alice", ApprovedAt: frozenNow()}}
	if diff := cmp.Diff(want, approvePub.published); diff != "" {
		t.Error("wrong ack published to thump.approvals (-want +got)", diff)
	}
	status := getStatus(t, dyn, "ar-1")
	if diff := cmp.Diff("Processed", status["phase"]); diff != "" {
		t.Error("wrong status.phase after reconcile (-want +got)", diff)
	}
}

func TestApprovalRequestController_Reconcile_RefusesToPublishWithoutAnAuthenticatedApprover(t *testing.T) {
	t.Parallel()
	// The approver annotation is stamped by a MutatingAdmissionPolicy that
	// only fires on UPDATE, and a companion policy refuses a CREATE that
	// arrives already decided. Absent both — policies uninstalled, or an
	// admission path that got around them — the only honest reading is that
	// nobody is recorded as having approved this, which is the one thing the
	// resource exists to establish.
	obj := approvalRequestObj("ar-2",
		map[string]interface{}{"signalRef": "fp-1", "decision": "approve"}, nil, nil)
	dyn := fakeDyn(t, obj)
	approvePub := &fakeApprovalPub{}
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: approvePub, Now: frozenNow}

	if err := c.ReconcileForTest(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
	if len(approvePub.published) != 0 {
		t.Error("an unattributed decision must publish nothing — a self-asserted approver is what the CR surface exists to replace")
	}
	status := getStatus(t, dyn, "ar-2")
	if status["phase"] != nil {
		t.Error("an unattributed decision must not be marked Processed — the stamp may still arrive on a later patch")
	}
}

func TestApprovalRequestController_Reconcile_ForceIsNotAValueThisSurfaceAccepts(t *testing.T) {
	t.Parallel()
	// Bypassing hiss's risk gate is a human substituting their judgment for
	// governance, and it stays with trim: a break-glass verb reachable by
	// changing five characters of an ordinary approval, on the same object
	// and under the same RBAC verb, is not break-glass.
	obj := approvalRequestObj("ar-3",
		map[string]interface{}{"signalRef": "fp-1", "decision": "force"},
		nil, map[string]string{"thump.dev/approved-by": "alice"})
	dyn := fakeDyn(t, obj)
	approvePub := &fakeApprovalPub{}
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: approvePub, Now: frozenNow}

	if err := c.ReconcileForTest(context.Background(), obj); err == nil {
		t.Fatal("force must be rejected by the controller, not just by the CRD's enum")
	}
	if len(approvePub.published) != 0 {
		t.Error("a rejected decision must publish nothing")
	}
}

func TestApprovalRequestController_Sweep_DeletesAProcessedRequestPastItsRetention(t *testing.T) {
	t.Parallel()
	// The sealed WAL is the audit record; this resource is a projection of a
	// hold. An unswept projection is one etcd object per hold, forever, which
	// is the cost D-1 declined CRD-as-output to avoid.
	decidedAt := frozenNow().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	obj := approvalRequestObj("ar-4",
		map[string]interface{}{"signalRef": "fp-1", "decision": "approve"},
		map[string]interface{}{"phase": "Processed", "decidedAt": decidedAt},
		map[string]string{"thump.dev/approved-by": "alice"})
	dyn := fakeDyn(t, obj)
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: &fakeApprovalPub{}, Now: frozenNow, Retention: 24 * time.Hour}

	c.ReconcileObjForTest(context.Background(), obj)

	_, err := dyn.Resource(approvalRequestGVR).Namespace("thump").Get(context.Background(), "ar-4", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("a Processed request past its retention must be swept, got err=%v", err)
	}
}

func TestApprovalRequestController_Sweep_KeepsAProcessedRequestInsideItsRetention(t *testing.T) {
	t.Parallel()
	decidedAt := frozenNow().Add(-time.Hour).UTC().Format(time.RFC3339)
	obj := approvalRequestObj("ar-5",
		map[string]interface{}{"signalRef": "fp-1", "decision": "approve"},
		map[string]interface{}{"phase": "Processed", "decidedAt": decidedAt},
		map[string]string{"thump.dev/approved-by": "alice"})
	dyn := fakeDyn(t, obj)
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: &fakeApprovalPub{}, Now: frozenNow, Retention: 24 * time.Hour}

	c.ReconcileObjForTest(context.Background(), obj)

	if _, err := dyn.Resource(approvalRequestGVR).Namespace("thump").Get(context.Background(), "ar-5", metav1.GetOptions{}); err != nil {
		t.Error("a recently-decided request must stay readable:", err)
	}
}

func TestApprovalRequestController_Reconcile_NoDecisionYetIsANoOp(t *testing.T) {
	t.Parallel()
	obj := approvalRequestObj("ar-4", map[string]interface{}{"signalRef": "fp-1"}, nil, nil)
	dyn := fakeDyn(t, obj)
	approvePub := &fakeApprovalPub{}
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: approvePub, Now: frozenNow}

	if err := c.ReconcileForTest(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
	if len(approvePub.published) != 0 {
		t.Error("no decision yet must publish nothing — most reconcile events are exactly this, not an error")
	}
	status := getStatus(t, dyn, "ar-4")
	if status["phase"] != nil {
		t.Error("no decision yet must not be marked Processed — there's nothing to skip on the next resync")
	}
}

func TestApprovalRequestController_Reconcile_AlreadyProcessedIsANoOp(t *testing.T) {
	t.Parallel()
	// This is the resync-idempotency property the whole design leans on: an
	// informer redelivers every object on every resync period, so reconcile
	// must be a no-op once status says the decision already went out —
	// otherwise a long-lived hiss process replays every ack once per resync
	// forever.
	obj := approvalRequestObj("ar-5",
		map[string]interface{}{"signalRef": "fp-1", "decision": "approve"},
		map[string]interface{}{"phase": "Processed"}, map[string]string{"thump.dev/approved-by": "alice"})
	dyn := fakeDyn(t, obj)
	approvePub := &fakeApprovalPub{}
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: approvePub, Now: frozenNow}

	if err := c.ReconcileForTest(context.Background(), obj); err != nil {
		t.Fatal(err)
	}
	if len(approvePub.published) != 0 {
		t.Error("an already-Processed object must never publish again on resync")
	}
}

func TestApprovalRequestController_Reconcile_UnrecognizedDecisionErrorsWithoutMarkingProcessed(t *testing.T) {
	t.Parallel()
	// The CRD's spec.decision enum should make this unreachable in a real
	// cluster; this pins the defensive path for the day the schema and the
	// code drift.
	obj := approvalRequestObj("ar-6",
		map[string]interface{}{"signalRef": "fp-1", "decision": "maybe"},
		nil, map[string]string{"thump.dev/approved-by": "alice"})
	dyn := fakeDyn(t, obj)
	c := &hiss.ApprovalRequestController{Dyn: dyn, Namespace: "thump",
		ApprovePub: &fakeApprovalPub{}, Now: frozenNow}

	if err := c.ReconcileForTest(context.Background(), obj); err == nil {
		t.Fatal("an unrecognized decision value must error")
	}
	status := getStatus(t, dyn, "ar-6")
	if status["phase"] != nil {
		t.Error("an errored reconcile must not be marked Processed — it should retry on the next resync")
	}
}
