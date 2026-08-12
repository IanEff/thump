package github_test

import (
	"crypto/sha1" //nolint:gosec // G505: content-addressing a test fixture, not a security use
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// pullState seeds the state a fake pull request starts a Withdraw test in.
type pullState int

const (
	pullAbsent pullState = iota
	pullOpen
	pullMerged
)

type fakePull struct {
	number int
	branch string
	state  string
	merged bool
}

// fakeGitHub is an in-memory stand-in for the slice of the GitHub REST API
// the github.Client calls: contents, refs, and pulls on one repo. It asserts
// the Authorization header on every request it serves, so no separate auth
// test is needed.
type fakeGitHub struct {
	*httptest.Server
	t *testing.T

	mu              sync.Mutex
	branches        map[string]string // branch -> ref sha
	files           map[string][]byte // "branch:path" -> content
	pulls           []*fakePull
	nextPull        int
	branchesCreated []string
	pullClosed      bool
	putCount        int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		t:        t,
		branches: map[string]string{"main": "basesha"},
		files:    map[string][]byte{"main:apps/demo/flagd.json": []byte(`{}`)},
		nextPull: 1,
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) PullsOpened() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pulls)
}

func (f *fakeGitHub) BranchesCreated() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.branchesCreated...)
}

func (f *fakeGitHub) PullClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pullClosed
}

func (f *fakeGitHub) PutCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.putCount
}

func (f *fakeGitHub) SeedPull(branch string, state pullState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state == pullAbsent {
		return
	}
	p := &fakePull{number: f.nextPull, branch: branch, state: "open"}
	f.nextPull++
	if state == pullMerged {
		p.state = "closed"
		p.merged = true
	}
	f.pulls = append(f.pulls, p)
}

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	const prefix = "/repos/acme/gitops/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case rest == "git/refs" && r.Method == http.MethodPost:
		f.createRef(w, r)
	case strings.HasPrefix(rest, "git/ref/heads/") && r.Method == http.MethodGet:
		f.getRef(w, strings.TrimPrefix(rest, "git/ref/heads/"))
	case strings.HasPrefix(rest, "git/refs/heads/") && r.Method == http.MethodDelete:
		f.deleteRef(w, strings.TrimPrefix(rest, "git/refs/heads/"))
	case strings.HasPrefix(rest, "contents/") && r.Method == http.MethodGet:
		f.getContents(w, r, strings.TrimPrefix(rest, "contents/"))
	case strings.HasPrefix(rest, "contents/") && r.Method == http.MethodPut:
		f.putContents(w, r, strings.TrimPrefix(rest, "contents/"))
	case rest == "pulls" && r.Method == http.MethodGet:
		f.listPulls(w, r)
	case rest == "pulls" && r.Method == http.MethodPost:
		f.createPull(w, r)
	case strings.HasPrefix(rest, "pulls/") && r.Method == http.MethodPatch:
		f.patchPull(w, strings.TrimPrefix(rest, "pulls/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) getRef(w http.ResponseWriter, branch string) {
	sha, ok := f.branches[branch]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": map[string]string{"sha": sha}})
}

func (f *fakeGitHub) createRef(w http.ResponseWriter, r *http.Request) {
	var body struct{ Ref, SHA string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Fatal(err)
	}
	branch := strings.TrimPrefix(body.Ref, "refs/heads/")
	f.branches[branch] = body.SHA
	if base, ok := f.files["main:apps/demo/flagd.json"]; ok {
		f.files[branch+":apps/demo/flagd.json"] = base
	}
	f.branchesCreated = append(f.branchesCreated, branch)
	writeJSON(w, http.StatusCreated, map[string]any{"ref": body.Ref})
}

func (f *fakeGitHub) deleteRef(w http.ResponseWriter, branch string) {
	delete(f.branches, branch)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeGitHub) getContents(w http.ResponseWriter, r *http.Request, path string) {
	ref := r.URL.Query().Get("ref")
	content, ok := f.files[ref+":"+path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"content": wrapBase64(content),
		"sha":     blobSHA(content),
	})
}

func (f *fakeGitHub) putContents(w http.ResponseWriter, r *http.Request, path string) {
	var body struct {
		Content, Branch, SHA string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Content)
	if err != nil {
		f.t.Fatal(err)
	}
	f.files[body.Branch+":"+path] = decoded
	f.putCount++
	writeJSON(w, http.StatusOK, map[string]any{"content": map[string]string{"sha": blobSHA(decoded)}})
}

func (f *fakeGitHub) listPulls(w http.ResponseWriter, r *http.Request) {
	head := r.URL.Query().Get("head")
	_, branch, _ := strings.Cut(head, ":")
	state := r.URL.Query().Get("state")

	var out []map[string]any
	for _, p := range f.pulls {
		if p.branch != branch {
			continue
		}
		if state != "all" && p.state != state {
			continue
		}
		out = append(out, pullJSON(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (f *fakeGitHub) createPull(w http.ResponseWriter, r *http.Request) {
	var body struct{ Head, Base, Body string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Fatal(err)
	}
	p := &fakePull{number: f.nextPull, branch: body.Head, state: "open"}
	f.nextPull++
	f.pulls = append(f.pulls, p)
	writeJSON(w, http.StatusCreated, pullJSON(p))
}

func (f *fakeGitHub) patchPull(w http.ResponseWriter, number string) {
	for _, p := range f.pulls {
		if fmt.Sprint(p.number) == number {
			p.state = "closed"
			f.pullClosed = true
		}
	}
	w.WriteHeader(http.StatusOK)
}

func pullJSON(p *fakePull) map[string]any {
	out := map[string]any{
		"number":   p.number,
		"state":    p.state,
		"html_url": fmt.Sprintf("https://github.com/acme/gitops/pull/%d", p.number),
	}
	if p.merged {
		out["merged_at"] = "2026-08-12T00:00:00Z"
	} else {
		out["merged_at"] = nil
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// wrapBase64 mimics GitHub's contents API, which wraps base64 at 60 columns.
func wrapBase64(content []byte) string {
	enc := base64.StdEncoding.EncodeToString(content)
	var b strings.Builder
	for i := 0; i < len(enc); i += 60 {
		end := min(i+60, len(enc))
		b.WriteString(enc[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}

func blobSHA(content []byte) string {
	return fmt.Sprintf("%x", sha1.Sum(content)) //nolint:gosec // G401: content-addressing a test fixture, not a security use
}
