package evidence_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/subjects"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubeChangeSource_Changes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 15, 12, 50, 0, 0, time.UTC)

	tests := map[string]struct {
		objects  []runtime.Object
		subjects subjects.SubjectIndex
		now      time.Time
		want     proposal.ChangeSnapshot
	}{
		"Changes names the topology node a changed ConfigMap belongs to rather than the Kubernetes object": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "otel-demo",
						Name:            "flagd-config",
						ResourceVersion: "123",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "kubectl-edit",
								Time:    &metav1.Time{Time: now.Add(-10 * time.Minute)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "flagd", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"}},
			},
			now: now,
			want: proposal.ChangeSnapshot{
				Events: []proposal.ChangeEvent{
					{
						ID:                  "config/otel-demo/flagd-config/123",
						Type:                "config",
						Target:              "flagd",
						Age:                 10 * time.Minute,
						HistoricalStaleness: 10 * time.Minute,
					},
				},
			},
		},
		"Changes reports a Deployment rollout as a deploy event carrying its generation": {
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  "otel-demo",
						Name:       "cart",
						Generation: 4,
					},
					Status: appsv1.DeploymentStatus{
						Conditions: []appsv1.DeploymentCondition{
							{
								Type:           appsv1.DeploymentProgressing,
								Status:         corev1.ConditionTrue,
								LastUpdateTime: metav1.Time{Time: now.Add(-15 * time.Minute)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "Deployment", Name: "cart"}},
			},
			now: now,
			want: proposal.ChangeSnapshot{
				Events: []proposal.ChangeEvent{
					{
						ID:                  "deploy/otel-demo/cart/4",
						Type:                "deploy",
						Target:              "cart",
						Age:                 15 * time.Minute,
						HistoricalStaleness: 15 * time.Minute,
					},
				},
			},
		},
		"Changes drops a change older than the lookback": {
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  "otel-demo",
						Name:       "cart",
						Generation: 4,
					},
					Status: appsv1.DeploymentStatus{
						Conditions: []appsv1.DeploymentCondition{
							{
								Type:           appsv1.DeploymentProgressing,
								Status:         corev1.ConditionTrue,
								LastUpdateTime: metav1.Time{Time: now.Add(-3 * time.Hour)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "Deployment", Name: "cart"}},
			},
			now:  now,
			want: proposal.ChangeSnapshot{},
		},
		"Changes ignores a ConfigMap whose only recent writer is thump itself": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "otel-demo",
						Name:            "flagd-config",
						ResourceVersion: "124",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "beat",
								Time:    &metav1.Time{Time: now.Add(-1 * time.Minute)},
							},
							{
								Manager: "thump-actuator",
								Time:    &metav1.Time{Time: now.Add(-30 * time.Second)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "flagd", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"}},
			},
			now:  now,
			want: proposal.ChangeSnapshot{},
		},
		"Changes still reports a ConfigMap thump touched when a human wrote it more recently": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "otel-demo",
						Name:            "flagd-config",
						ResourceVersion: "125",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "thump-actuator",
								Time:    &metav1.Time{Time: now.Add(-10 * time.Minute)},
							},
							{
								Manager: "kubectl-edit",
								Time:    &metav1.Time{Time: now.Add(-2 * time.Minute)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "flagd", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"}},
			},
			now: now,
			want: proposal.ChangeSnapshot{
				Events: []proposal.ChangeEvent{
					{
						ID:                  "config/otel-demo/flagd-config/125",
						Type:                "config",
						Target:              "flagd",
						Age:                 2 * time.Minute,
						HistoricalStaleness: 2 * time.Minute,
					},
				},
			},
		},
		"Changes drops a change whose coordinates resolve to no subject": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "otel-demo",
						Name:            "untracked-cm",
						ResourceVersion: "1",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "kubectl-edit",
								Time:    &metav1.Time{Time: now.Add(-5 * time.Minute)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "flagd", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"}},
			},
			now:  now,
			want: proposal.ChangeSnapshot{},
		},
		"Changes lists only the namespaces the subject rules name": {
			objects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "other-ns",
						Name:            "other-cm",
						ResourceVersion: "1",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "kubectl-edit",
								Time:    &metav1.Time{Time: now.Add(-5 * time.Minute)},
							},
						},
					},
				},
			},
			subjects: subjects.SubjectIndex{
				{Subject: "flagd", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"}},
			},
			now:  now,
			want: proposal.ChangeSnapshot{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := evidence.KubeChangeSource{
				Client:   fake.NewSimpleClientset(tc.objects...),
				Subjects: tc.subjects,
				Now:      func() time.Time { return tc.now },
			}
			got, err := src.Changes(t.Context(), signal.Detection{OriginService: "cart"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong change snapshot (-want +got)\n", diff)
			}
		})
	}
}
