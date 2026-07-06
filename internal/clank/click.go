package clank

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ianeff/thump/api/v1/outcome"
	"sigs.k8s.io/yaml"
)

var (
	ErrUnauditableOutcome = errors.New("click: outcome fails its audit invariant")
	ErrIncoherentOutcome  = errors.New("click: outcome mode and result disagree")
)

type Click struct {
	Ledger *MemProposalLog
	Cases  *CaseBase
}

func (c *Click) Absorb(ctx context.Context, o outcome.Outcome) error {
	if err := o.Auditable(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnauditableOutcome, err)
	}
	if !coherent(o) {
		return fmt.Errorf("%w: mode %q claims result %q", ErrIncoherentOutcome, o.Mode, o.Result)
	}
	set, err := c.Ledger.Observe(ctx, o)
	if err != nil {
		return err
	}
	return c.Cases.Append(newCase(set, o))
}

func coherent(o outcome.Outcome) bool {
	if o.Mode == outcome.ModeDryRun {
		return o.Result == outcome.ResultRendered
	}
	return o.Result != outcome.ResultRendered
}

func newCase(set ProposalSet, o outcome.Outcome) Case {
	var conf float64
	for _, cand := range set.Proposals {
		if cand.ID == set.Recommended {
			conf = cand.Confidence
		}
	}
	return Case{
		Fingerprint:  o.SignalRef,
		DecisionRef:  o.DecisionRef,
		OutcomeRef:   o.ID,
		ContractRef:  o.ContractRef,
		FailureClass: set.FailureClass,
		Confidence:   conf,
		Mode:         o.Mode,
		Result:       o.Result,
		ObservedAt:   o.ExecutedAt,
	}
}

type ReturnEdge struct {
	Inbox string
	Click Click
}

func (re *ReturnEdge) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	matches, err := filepath.Glob(filepath.Join(re.Inbox, "*.yaml"))
	if err != nil {
		return fmt.Errorf("click: list inbox: %w", err)
	}

	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return fmt.Errorf("click: read %s: %w", path, err)
		}

		var o outcome.Outcome
		if err := yaml.Unmarshal(raw, &o); err != nil {
			if qErr := re.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("click: quarantine %s: %w", path, qErr)
			}
			continue
		}

		switch err := re.Click.Absorb(ctx, o); {
		case err == nil:
			if pErr := re.disposition(path, "processed"); pErr != nil {
				return fmt.Errorf("click: archive %s: %w", path, pErr)
			}
		case errors.Is(err, ErrNoOpenSet):
			if uErr := re.disposition(path, "unmatched"); uErr != nil {
				return fmt.Errorf("click: unmatch %s: %w", path, uErr)
			}
		default: // unauditable / incoherent
			if qErr := re.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("click: quarantine %s: %w", path, qErr)
			}
		}
	}
	return nil
}

func (re *ReturnEdge) disposition(path, sub string) error {
	dir := filepath.Join(re.Inbox, sub)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}
