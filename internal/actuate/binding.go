package actuate

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/ianeff/thump/internal/contract"
)

// ErrUnbindable is the load-time refusal: an authored contract naming no
// reachable mechanism fails at startup, not the first time governance
// approves it.
var ErrUnbindable = errors.New("actuate: contract names no executable mechanism")

// bind maps every contract in a StaticCatalog to its concrete mutation.
func bind(cat *contract.StaticCatalog, forgeWired bool) (map[string]binding, error) {
	out := make(map[string]binding)
	for _, c := range cat.Contracts() {
		b, err := bindingFor(c, forgeWired)
		if err != nil {
			return nil, err
		}
		out[c.Name] = b
	}
	return out, nil
}

// bindingFor turns one contract's authored execution block into the
// pair of mutations Run dispatches.
func bindingFor(c contract.ActionContract, forgeWired bool) (binding, error) {
	forward, err := opFor(c.Execution.Forward, forgeWired)
	if err != nil {
		return binding{}, fmt.Errorf("contract %q forward: %w", c.Name, err)
	}
	reverse, err := opFor(c.Execution.Reverse, forgeWired)
	if err != nil {
		return binding{}, fmt.Errorf("contract %q reverse: %w", c.Name, err)
	}
	return binding{forward: forward, reverse: reverse}, nil
}

// opFor collapses an authored step list into one operation.
func opFor(steps []contract.Step, forgeWired bool) (operation, error) {
	switch len(steps) {
	case 0:
		return nil, fmt.Errorf("no steps authored: %w", ErrUnbindable)
	case 1:
		return mechanismFor(steps[0], forgeWired)
	}
	seq := make(seqOp, 0, len(steps))
	for i, s := range steps {
		op, err := mechanismFor(s, forgeWired)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		seq = append(seq, op)
	}
	return seq, nil
}

// mechanismFor selects the compiled mutation a step names. The verb set is
// closed, and every required target is checked here — this switch is where
// the autonomy boundary stays in Go while the table lives in YAML.
func mechanismFor(s contract.Step, forgeWired bool) (operation, error) {
	switch s.Verb {
	case "exec":
		if s.Namespace == "" || s.Selector == "" || len(s.Command) == 0 {
			return nil, fmt.Errorf("exec needs namespace, selector and command: %w", ErrUnbindable)
		}
		return execOp{namespace: s.Namespace, selector: s.Selector, command: s.Command}, nil
	case "scale":
		if s.Namespace == "" || s.Deployment == "" || s.Replicas == nil {
			return nil, fmt.Errorf("scale needs namespace, deployment and replicas: %w", ErrUnbindable)
		}
		return scaleOp{namespace: s.Namespace, deployment: s.Deployment, replicas: *s.Replicas}, nil
	case "restart":
		if s.Namespace == "" || s.Deployment == "" {
			return nil, fmt.Errorf("restart needs namespace and deployment: %w", ErrUnbindable)
		}
		return restartOp{namespace: s.Namespace, deployment: s.Deployment}, nil
	case "flagVariant":
		if s.Namespace == "" || s.ConfigMap == "" || s.DataKey == "" || s.Flag == "" || s.Variant == "" {
			return nil, fmt.Errorf("flagVariant needs namespace, configMap, dataKey, flag and variant: %w", ErrUnbindable)
		}
		return flagVariantOp{
			namespace: s.Namespace, configMap: s.ConfigMap, dataKey: s.DataKey,
			flag: s.Flag, variant: s.Variant,
		}, nil
	case "maintenanceRelease":
		if !forgeWired {
			return nil, fmt.Errorf("maintenanceRelease needs a GitOps source of record - set FORGE_REPO and FORGE_TOKEN: %w", ErrUnbindable)
		}
		if s.Path == "" || s.Flag == "" || s.Variant == "" {
			return nil, fmt.Errorf("maintenanceRelease needs path, flag, and variant: %w", ErrUnbindable)
		}
		return maintenanceReleaseOp{path: s.Path, flag: s.Flag, variant: s.Variant}, nil
	default:
		return nil, fmt.Errorf("verb %q has no mechanism: %w", s.Verb, ErrUnbindable)
	}
}

// BoundRefs returns the contract refs cat can actually execute, sorted for a
// stable comparison — a property of the catalog, independent of runtime
// wiring. It always binds as if a forge were wired: a missing forge is
// newWithTimeout's refusal to make, not a reason for a release contract to
// silently disappear from a caller asking what the catalog authors.
func BoundRefs(cat *contract.StaticCatalog) ([]string, error) {
	b, err := bind(cat, true)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(b)), nil
}
