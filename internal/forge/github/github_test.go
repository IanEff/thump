package github_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/forge"
	"github.com/ianeff/thump/internal/forge/github"
)

// TestCut_OpensOnePullRequestPerReleaseKeyAndUpdatesItOnRedelivery pins the
// idempotency Release.Key exists to provide. A redelivered decision must land
// on the open release rather than opening a second one — two open releases
// against the same path contradict each other, and a reviewer merging the
// stale one re-applies a remedy nobody chose.
func TestCut_OpensOnePullRequestPerReleaseKeyAndUpdatesItOnRedelivery(t *testing.T) {
	t.Parallel()

	api := newFakeGitHub(t)
	c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: api.URL, Client: api.Client()}

	rel := forge.Release{
		Key:     "disable-cart-failure-release",
		Path:    "apps/demo/flagd.json",
		Content: []byte(`{"flags":{"cartFailure":{"defaultVariant":"off"}}}`),
		Notes:   "dummy notes",
	}
	first, err := c.Cut(t.Context(), rel)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Cut(t.Context(), rel)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(first, second); diff != "" {
		t.Error("a redelivered release opened a different pull request", diff)
	}
	if diff := cmp.Diff(1, api.PullsOpened()); diff != "" {
		t.Error("wrong number of pull requests for two deliveries of one key", diff)
	}
}

// TestCut_KeysTheRevertToItsOwnBranchGitWillAccept pins both halves of the
// revert's identity at once: it is a second review against the same path, so
// it cannot share the forward release's branch — and the key it derives from
// carries a colon, which git refuses in a ref name.
func TestCut_KeysTheRevertToItsOwnBranchGitWillAccept(t *testing.T) {
	t.Parallel()

	api := newFakeGitHub(t)
	c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: api.URL, Client: api.Client()}

	for _, key := range []string{"disable-cart-failure-release", "disable-cart-failure-release:revert"} {
		rel := forge.Release{Key: key, Path: "apps/demo/flagd.json", Content: []byte(`{"flags":{}}`), Notes: "dummy notes"}
		if _, err := c.Cut(t.Context(), rel); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"thump/disable-cart-failure-release", "thump/disable-cart-failure-release-revert"}
	if diff := cmp.Diff(want, api.BranchesCreated()); diff != "" {
		t.Error("wrong branches for a forward release and its revert", diff)
	}
	for _, b := range api.BranchesCreated() {
		if strings.ContainsAny(b, ":~^?*[\\ ") {
			t.Errorf("branch %q is not a name git will accept as a ref", b)
		}
	}
}

// TestCut_SkipsTheCommitWhenContentIsByteIdenticalToTheCurrentBlob pins that
// a redelivery touches nothing when the flag is already at the desired
// value — a commit that changes no bytes is stale-release noise for the
// human reviewing it, not a new decision.
func TestCut_SkipsTheCommitWhenContentIsByteIdenticalToTheCurrentBlob(t *testing.T) {
	t.Parallel()

	api := newFakeGitHub(t)
	c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: api.URL, Client: api.Client()}

	rel := forge.Release{Key: "disable-cart-failure-release", Path: "apps/demo/flagd.json", Content: []byte(`{}`), Notes: "dummy notes"}
	if _, err := c.Cut(t.Context(), rel); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(0, api.PutCount()); diff != "" {
		t.Error("wrong PUT count when the branch is cut with content matching the base blob", diff)
	}
}

// TestWithdraw_ReportsAcceptanceWithoutTouchingAReleaseAHumanAlreadyMerged
// pins the distinction the acceptance poll rests on. A merged release was
// applied and cannot be retracted; an open one applied nothing, so retracting
// it is stale-release hygiene rather than an undo.
func TestWithdraw_ReportsAcceptanceWithoutTouchingAReleaseAHumanAlreadyMerged(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		seed         pullState
		wantAccepted bool
		wantClosed   bool
	}{
		"Withdraw reports accepted and closes nothing when a human already merged the release": {
			seed: pullMerged, wantAccepted: true, wantClosed: false,
		},
		"Withdraw retracts the release and reports not-accepted when it is still open": {
			seed: pullOpen, wantAccepted: false, wantClosed: true,
		},
		"Withdraw reports not-accepted when no release was ever cut for the key": {
			seed: pullAbsent, wantAccepted: false, wantClosed: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			api := newFakeGitHub(t)
			api.SeedPull("thump/disable-cart-failure-release", tc.seed)
			c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: api.URL, Client: api.Client()}

			accepted, err := c.Withdraw(t.Context(), "disable-cart-failure-release")
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tc.wantAccepted, accepted); diff != "" {
				t.Error("wrong acceptance verdict for the release", diff)
			}
			if diff := cmp.Diff(tc.wantClosed, api.PullClosed()); diff != "" {
				t.Error("wrong retraction behaviour for the release", diff)
			}
		})
	}
}

// TestRead_DecodesContentGitHubWrapsAt60Columns pins that Read strips the
// newlines GitHub's contents API inserts into base64 — a real response
// fails silently against a decoder that assumes an unwrapped string.
func TestRead_DecodesContentGitHubWrapsAt60Columns(t *testing.T) {
	t.Parallel()

	api := newFakeGitHub(t)
	c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: api.URL, Client: api.Client()}

	got, err := c.Read(t.Context(), "apps/demo/flagd.json")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]byte(`{}`), got); diff != "" {
		t.Error("wrong content decoded from a wrapped base64 response", diff)
	}
}

// TestRead_ReturnsErrNotFoundOnA404 pins that a missing path is reported as
// the sentinel every caller here checks with errors.Is, not a generic error
// string a caller would have to pattern-match.
func TestRead_ReturnsErrNotFoundOnA404(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: ts.URL, Client: ts.Client()}

	_, err := c.Read(t.Context(), "apps/demo/flagd.json")
	if !errors.Is(err, github.ErrNotFound) {
		t.Errorf("want ErrNotFound reading an absent path, got %v", err)
	}
}

// TestRead_ReturnsErrForgeStatusOnA500 pins that a refused request is
// reported rather than swallowed — a 500 is not "absent," and treating it
// like one would let a maintenance release believe a flag file doesn't
// exist when the API is simply down.
func TestRead_ReturnsErrForgeStatusOnA500(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	c := &github.Client{Repo: "acme/gitops", Token: "dummy token", BaseURL: ts.URL, Client: ts.Client()}

	_, err := c.Read(t.Context(), "apps/demo/flagd.json")
	if !errors.Is(err, github.ErrForgeStatus) {
		t.Errorf("want ErrForgeStatus reading through a 500, got %v", err)
	}
}
