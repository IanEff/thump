package proposal_test

import (
	"testing"

	"github.com/ianeff/thump/api/v1/proposal"
)

func TestSet_ContractRefFor(t *testing.T) {
	t.Parallel()
	set := proposal.Set{
		Proposals: []proposal.Candidate{
			{ID: "p1", ContractRef: "throttle-non-critical-paths"},
			{ID: "p2", ContractRef: "scale-out-rgw-gateways"},
		},
	}

	cases := map[string]struct {
		set  proposal.Set
		id   string
		want string
	}{
		"a matching ID resolves to its ContractRef": {
			set: set, id: "p2", want: "scale-out-rgw-gateways",
		},
		"an ID absent from Proposals resolves empty": {
			set: set, id: "no-such-id", want: "",
		},
		"an empty Proposals list resolves empty, not a panic": {
			set: proposal.Set{}, id: "p1", want: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.set.ContractRefFor(tc.id); got != tc.want {
				t.Errorf("ContractRefFor(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
