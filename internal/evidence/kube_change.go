package evidence

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/subjects"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DefaultIgnoredManagers are the field managers whose writes are never change
// events. thump's own actuator patches the objects its catalog authorises, and a
// fresh self-write is a maximally-recent change targeting a node in the burning
// service's topology — so unfiltered, the engine's own remediation would
// corroborate its next decision about the same signal.
var DefaultIgnoredManagers = []string{"beat", "thump-actuator"}

// KubeChangeSource reports recent Deployment rollouts and ConfigMap edits in the
// namespaces the subject rules name, as change events the causal scorer can score.
type KubeChangeSource struct {
	Client         kubernetes.Interface
	Subjects       subjects.SubjectIndex
	ChangeLookback time.Duration    // 0 -> DefaultChangeLookback
	IgnoreManagers []string         // nil -> DefaultIgnoredManagers
	Now            func() time.Time // nil -> time.Now
}

var _ reason.ChangeSource = (*KubeChangeSource)(nil)

// Changes queries Deployments and ConfigMaps across every namespace the authored
// subject rules name — bounded to authored namespaces rather than listing
// cluster-wide, which keeps both RBAC and API server load proportional to the
// rig's configured scope.
func (k KubeChangeSource) Changes(ctx context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	now := clock(k.Now)()
	lookback := k.ChangeLookback
	if lookback <= 0 {
		lookback = DefaultChangeLookback
	}

	ignoreManagers := k.IgnoreManagers
	if ignoreManagers == nil {
		ignoreManagers = DefaultIgnoredManagers
	}

	namespaces := k.namespaces()
	if len(namespaces) == 0 {
		return proposal.ChangeSnapshot{}, nil
	}

	var events []proposal.ChangeEvent

	for _, ns := range namespaces {
		deployments, err := k.Client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return proposal.ChangeSnapshot{}, fmt.Errorf("list deployments in %s: %w", ns, err)
		}
		for _, d := range deployments.Items {
			for _, cond := range d.Status.Conditions {
				if cond.Type != appsv1.DeploymentProgressing || cond.LastUpdateTime.IsZero() {
					continue
				}
				age := now.Sub(cond.LastUpdateTime.Time)
				if age < 0 || age > lookback {
					continue
				}
				target := k.Subjects.For(subjects.Coordinates{Namespace: d.Namespace, Kind: "Deployment", Name: d.Name})
				if target == "" {
					continue
				}
				events = append(events, proposal.ChangeEvent{
					ID:                  fmt.Sprintf("deploy/%s/%s/%d", d.Namespace, d.Name, d.Generation),
					Type:                "deploy",
					Target:              target,
					Age:                 age,
					HistoricalStaleness: age,
				})
			}
		}

		configMaps, err := k.Client.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return proposal.ChangeSnapshot{}, fmt.Errorf("list configmaps in %s: %w", ns, err)
		}
		for _, cm := range configMaps.Items {
			var maxTime time.Time
			for _, mf := range cm.ManagedFields {
				if slices.Contains(ignoreManagers, mf.Manager) || mf.Time == nil || mf.Time.IsZero() {
					continue
				}
				if maxTime.IsZero() || mf.Time.After(maxTime) {
					maxTime = mf.Time.Time
				}
			}
			if maxTime.IsZero() {
				continue
			}
			age := now.Sub(maxTime)
			if age < 0 || age > lookback {
				continue
			}
			target := k.Subjects.For(subjects.Coordinates{Namespace: cm.Namespace, Kind: "ConfigMap", Name: cm.Name})
			if target == "" {
				continue
			}
			events = append(events, proposal.ChangeEvent{
				ID:                  fmt.Sprintf("config/%s/%s/%s", cm.Namespace, cm.Name, cm.ResourceVersion),
				Type:                "config",
				Target:              target,
				Age:                 age,
				HistoricalStaleness: age,
			})
		}
	}

	return proposal.ChangeSnapshot{Events: events}, nil
}

// namespaces extracts the unique, non-empty namespace names declared across
// the index, sorted so namespace iteration order is deterministic.
func (k KubeChangeSource) namespaces() []string {
	seen := make(map[string]struct{}, len(k.Subjects))
	var list []string
	for _, rule := range k.Subjects {
		ns := rule.Namespace
		if ns == "" {
			continue
		}
		if _, ok := seen[ns]; !ok {
			seen[ns] = struct{}{}
			list = append(list, ns)
		}
	}
	slices.Sort(list)
	return list
}
