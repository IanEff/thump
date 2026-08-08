package tune

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/rca"
	"github.com/ianeff/thump/internal/replay"
)

// SweepConfig is one sweep's inputs. Transcripts are pairs of paths, not a
// directory, so a run that captured no set cannot silently be swept.
type SweepConfig struct {
	Transcripts []TranscriptPaths
	Corpus      clank.Corpus
	Steps       int // grid points per dimension; 5 gives 0.1 resolution over 0.3-0.7
	// Objective is what this sweep maximizes.  Zero value means
	// DefaultObjective.
	Objective Objective
	// Cases are the labeled answers keyed to transcripts by
	// rca.TranscriptName.
	Cases []rca.Case
}

// TranscriptPaths is one replay fixture: the conversation and the set it
// emitted.
type TranscriptPaths struct {
	JSONL string
	Set   string
}

// Point is one grid position and what it produced.
type Point struct {
	GroundingNone  float64
	GroundingOne   float64
	MeanConfidence float64
	Moved          int // how many transcripts changed confidence vs the default
	// Grounded is how many labelled rows this grid point actually
	// passes-- the objective.
	Grounded int
}

// Run sweeps GroundingNone and GroundingOne over cfg.Transcripts and returns
// every grid point. It spends no tokens: every model answer comes from the
// recording. A transcript that fails to replay under the default weights is
// dropped before the sweep starts, so one broken fixture doesn't abort every
// grid point. GroundingMany and Causal are deliberately not swept — no
// recorded row corroborates on two backends in this harness, so both
// surfaces are flat and a recommendation over them would be meaningless.
func Run(ctx context.Context, cfg SweepConfig) ([]Point, error) {
	if len(cfg.Transcripts) == 0 {
		return nil, fmt.Errorf("tune: no transcripts — a sweep with nothing to replay reports its own defaults back")
	}
	steps := cfg.Steps
	if steps < 2 {
		steps = 5
	}

	loaded := make([]replay.Transcript, 0, len(cfg.Transcripts))
	for _, p := range cfg.Transcripts {
		tr, err := replay.LoadTranscript(p.JSONL, p.Set)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, tr)
	}

	// DefaultScoringWeights, not a loaded config/clank/weights.yaml: the sweep's
	// non-swept dimensions should start from the same point replay/rca do by
	// default. TestDefaultScoringWeights_MatchesTheShippedConfig
	// (internal/clank) keeps this equal to the shipped file.
	base := clank.DefaultScoringWeights()

	usable := loaded[:0]
	for _, tr := range loaded {
		if _, err := replay.Propose(ctx, tr, base); err != nil {
			slog.Warn("tune: dropping transcript that failed to replay", "fingerprint", tr.Set.SignalRef, "err", err)
			continue
		}
		usable = append(usable, tr)
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("tune: every transcript failed to replay under default weights")
	}

	byStem := make(map[string]rca.Case, len(cfg.Cases))
	for _, c := range cfg.Cases {
		byStem[rca.TranscriptName(c)] = c
	}
	if len(cfg.Cases) > 0 {
		matched := false
		for _, tr := range usable {
			if _, ok := byStem[strings.TrimSuffix(filepath.Base(tr.Path), ".jsonl")]; ok {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("tune: no transcript pairs to a graded case — a sweep with no labels scores an unlabelled mean and calls it a recommendation")
		}
	}

	obj := cfg.Objective
	if obj.Grounded == nil {
		obj = DefaultObjective()
	}

	var points []Point
	for i := range steps {
		for j := range steps {
			w := base
			w.GroundingNone = 0.1 + 0.1*float64(i)
			w.GroundingOne = 0.4 + 0.1*float64(j)

			var sum float64
			var n, moved int
			var rows []rca.Row
			for _, tr := range usable {
				set, err := replay.Propose(ctx, tr, w)
				if err != nil {
					return nil, err
				}
				if len(set.Proposals) > 0 {
					sum += set.Proposals[0].ComputedConfidence
					n++
					if len(tr.Set.Proposals) > 0 &&
						set.Proposals[0].ComputedConfidence != tr.Set.Proposals[0].ComputedConfidence {
						moved++
					}
				}

				stem := strings.TrimSuffix(filepath.Base(tr.Path), ".jsonl")
				if c, ok := byStem[stem]; ok {
					rows = append(rows, rca.Grade(c, set))
				}
			}
			p := Point{
				GroundingNone: w.GroundingNone,
				GroundingOne:  w.GroundingOne,
				Moved:         moved,
				Grounded:      obj.Grounded(rca.NewReport(rows)),
			}
			if n > 0 {
				p.MeanConfidence = sum / float64(n)
			}
			points = append(points, p)
		}
	}
	return points, nil
}
