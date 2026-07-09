package clank

import (
	"context"
	"fmt"
	"io"

	"github.com/ianeff/thump/api/v1/proposal"
	"sigs.k8s.io/yaml"
)

// MarkdownPublisher renders a proposal.Set as a short human-readable
// summary — the failure class, how many candidates were considered, and
// which one is Recommended. It's a rendering aid, not the audit trail: the
// full set, including candidates it didn't recommend, is what
// MemProposalLog.Record keeps.
type MarkdownPublisher struct {
	W io.Writer
}

func (s *MarkdownPublisher) Publish(_ context.Context, _ string, ps proposal.Set) error {
	if _, err := fmt.Fprintf(s.W, "## ProposalSet: %s (%d considered)\n", ps.FailureClass, len(ps.Proposals)); err != nil {
		return err
	}
	for _, c := range ps.Proposals {
		if c.ID == ps.Recommended {
			_, err := fmt.Fprintf(s.W, "**Recommended:** %s — %s\n", c.ID, c.ContractRef)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// YAMLPublisher writes a proposal.Set to W as YAML in full — every candidate,
// hypothesis, and evidence ref, not just the recommendation. This is the
// shape the dir-poll transport drops into its outbox, for hiss to read the
// whole set back in.
type YAMLPublisher struct {
	W io.Writer
}

func (s *YAMLPublisher) Publish(_ context.Context, _ string, ps proposal.Set) error {
	out, err := yaml.Marshal(ps)
	if err != nil {
		return fmt.Errorf("yaml publisher: marshal proposal set: %w", err)
	}
	if _, err := s.W.Write(out); err != nil {
		return fmt.Errorf("yaml publisher: write: %w", err)
	}
	return nil
}

// proposalFilename names an emitted ProposalSet on disk by its fingerprint, so
// a re-proposal of the same incident overwrites rather than piling up. It falls
// back to the set's Name if a set somehow has no fingerprint.
func proposalFilename(ps proposal.Set) string {
	if ps.SignalRef != "" {
		return ps.SignalRef
	}
	return ps.Name
}
