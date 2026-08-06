package tune

import (
	"context"
	"fmt"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/replay"
)

// SweepConfig is one sweep's inputs. Transcripts are pairs of paths, not a
// directory, so a run that captured no set cannot silently be swept.
type SweepConfig struct {
	Transcripts []TranscriptPaths
	Corpus      clank.Corpus
	Steps       int // grid points per dimension; 5 gives 0.1 resolution over 0.3-0.7
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
}

// Run sweeps GroundingNone and GroundingOne over cfg.Transcripts and returns
// every grid point. It spends no tokens: every model answer comes from the
// recording. GroundingMany and Causal are deliberately not swept — no
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

	base := clank.DefaultScoringWeights()
	var points []Point
	for i := range steps {
		for j := range steps {
			w := base
			w.GroundingNone = 0.1 + 0.1*float64(i)
			w.GroundingOne = 0.4 + 0.1*float64(j)

			var sum float64
			var n, moved int
			for _, tr := range loaded {
				set, err := replay.Propose(ctx, tr, w)
				if err != nil {
					return nil, err
				}
				if len(set.Proposals) == 0 {
					continue
				}
				sum += set.Proposals[0].ComputedConfidence
				n++
				if len(tr.Set.Proposals) > 0 &&
					set.Proposals[0].ComputedConfidence != tr.Set.Proposals[0].ComputedConfidence {
					moved++
				}
			}
			p := Point{GroundingNone: w.GroundingNone, GroundingOne: w.GroundingOne, Moved: moved}
			if n > 0 {
				p.MeanConfidence = sum / float64(n)
			}
			points = append(points, p)
		}
	}
	return points, nil
}
