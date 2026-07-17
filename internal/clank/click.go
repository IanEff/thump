package clank

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"sigs.k8s.io/yaml"
)

var (
	ErrUnauditableOutcome = errors.New("click: outcome fails its audit invariant")
	ErrIncoherentOutcome  = errors.New("click: outcome mode and result disagree")
)

// Click is the Learn edge: it absorbs one outcome.Outcome, closes the
// proposal.Set it answers to, and appends what happened as a Case so future
// scoring can read it back through Prior. Absorb only records what already
// happened — it forms no new belief on its own; CaseBase.Alignment is what
// turns repeated corroboration into a raised prior.
type Click struct {
	Ledger   *MemProposalLog
	Cases    *CaseBase
	Recorder *Recorder
}

// Absorb closes the loop for one outcome. It rejects o outright if it fails
// its own Auditable invariant, or if its Mode and Result disagree (a dry-run
// outcome must render; every other mode must not), then hands it to the
// ledger's Observe and appends the result as a Case. ErrNoOpenSet — o arrived
// with nothing open to answer to — is an expected outcome of the return
// edge, not a defect; the caller decides whether that's terminal.
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
	if c.Recorder != nil {
		c.Recorder.recordResolution(set, o)
		c.Recorder.recordCalibration(set, o)
		c.Recorder.recordEffectiveness(set, o)
	}
	return c.Cases.Append(newCase(set, o))
}

func coherent(o outcome.Outcome) bool {
	if o.Mode == outcome.ModeDryRun {
		return o.Result == outcome.ResultRendered
	}
	return o.Result != outcome.ResultRendered
}

func newCase(set proposal.Set, o outcome.Outcome) Case {
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

// ReturnEdge is click's transport: a directory poll that reads outcome YAML
// files out of Inbox and hands each to Click.Absorb. It mirrors Transport's
// forward-path shape — glob, dispose, never let one bad file block the rest
// of the queue.
type ReturnEdge struct {
	Inbox string
	Click Click
}

// Tick processes every outcome file currently in Inbox once: a file that
// fails to unmarshal is quarantined; one Absorb accepts is filed processed;
// one with no open set to answer to (ErrNoOpenSet) is filed unmatched, not
// an error — the set may have already closed for another reason; anything
// else Absorb rejects is quarantined.
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
