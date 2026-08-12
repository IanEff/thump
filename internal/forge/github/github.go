// Package github cuts a corrective maintenance release as a pull request —
// the concrete Forge thump injects at wiring time, kept in its own package so
// the HTTP client lives outside the actuator, never inside it. One release
// key is one pull request: a redelivery updates it rather than opening a
// second.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ianeff/thump/internal/forge"
	"github.com/ianeff/thump/internal/httpx"
)

// ErrForgeStatus is returned when GitHub answers with a non-2xx that isn't
// a 404 — the request was built and sent, and refusing it is reported rather
// than swallowed.
var ErrForgeStatus = errors.New("github: api returned a non-2xx status")

// ErrNotFound is a 404, kept distinct from ErrForgeStatus because every
// caller here distinguishes absent from refused — a branch that doesn't
// exist yet is the ordinary first-cut path, not an outage.
var ErrNotFound = errors.New("github: not found")

// Client publishes releases to one repository. The token reaches that
// repository and no other, which is the entire blast-radius argument for
// this credential — so no method takes a repo parameter that could widen it
// at call time.
type Client struct {
	Repo    string       // "owner/name" — the only repository this credential may reach
	Token   string       // fine-grained PAT, contents+pull-requests write on Repo
	Branch  string       // the base branch releases target; empty means "main"
	BaseURL string       // empty means https://api.github.com; set to point a test at an httptest server
	Client  *http.Client // nil uses httpx.Client(httpx.DefaultBackendTimeout, nil)
}

// Read returns the current bytes at path on the base branch.
func (c *Client) Read(ctx context.Context, path string) ([]byte, error) {
	content, _, err := c.blobAt(ctx, path, c.base())
	return content, err
}

// Cut publishes rel for review and returns where a human can find it. A
// redelivery of the same rel.Key lands on the branch and pull request the
// first delivery opened rather than opening a second, and a byte-identical
// rel.Content skips the commit entirely — a redelivery should touch nothing.
func (c *Client) Cut(ctx context.Context, rel forge.Release) (string, error) {
	branch := branchFor(rel.Key)
	base := c.base()

	if _, err := c.refSHA(ctx, branch); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return "", err
		}
		baseSHA, err := c.refSHA(ctx, base)
		if err != nil {
			return "", fmt.Errorf("github: base ref %s: %w", base, err)
		}
		if err := c.createRef(ctx, branch, baseSHA); err != nil {
			return "", err
		}
	}

	current, sha, err := c.blobAt(ctx, rel.Path, branch)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if !bytes.Equal(current, rel.Content) {
		if err := c.putContents(ctx, rel.Path, branch, rel.Content, sha); err != nil {
			return "", err
		}
	}

	pr, err := c.findPull(ctx, branch, "open")
	if err != nil {
		return "", err
	}
	if pr != nil {
		return pr.HTMLURL, nil
	}
	return c.createPull(ctx, branch, base, rel.Notes)
}

// Withdraw retracts the release for key if it is still open and reports
// whether it had already been accepted. A merged release was applied and
// cannot be retracted, so Withdraw makes no mutating call against one — that
// is the one HTTP request this method must never send.
func (c *Client) Withdraw(ctx context.Context, key string) (bool, error) {
	branch := branchFor(key)
	pr, err := c.findPull(ctx, branch, "all")
	if err != nil {
		return false, err
	}
	if pr == nil {
		return false, nil
	}
	if pr.MergedAt != nil {
		return true, nil
	}
	if pr.State == "open" {
		if err := c.closePull(ctx, pr.Number); err != nil {
			return false, err
		}
		if err := c.deleteRef(ctx, branch); err != nil {
			return false, err
		}
	}
	return false, nil
}

// branchFor is the git ref one release lives on. The release key is not
// usable as a ref: releaseKey spells a revert with a colon, which git
// refuses in a ref name, so the separator is rewritten here rather than the
// key being reshaped — the key is what collapses a redelivery onto one
// release and must not drift to suit a branch-naming rule.
func branchFor(key string) string {
	return "thump/" + strings.ReplaceAll(key, ":", "-")
}

func (c *Client) base() string {
	if c.Branch != "" {
		return c.Branch
	}
	return "main"
}

func (c *Client) owner() string {
	owner, _, _ := strings.Cut(c.Repo, "/")
	return owner
}

func (c *Client) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return httpx.Client(httpx.DefaultBackendTimeout, nil)
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.github.com"
}

// do sends one API request and returns the raw response for the caller to
// interpret — 404 is an ordinary outcome for several of these endpoints, so
// status handling stays with the caller rather than being collapsed here.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("github: marshal %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("github: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	return resp, nil
}

// statusError classifies a non-2xx response: a 404 is ErrNotFound, anything
// else is ErrForgeStatus.
func statusError(resp *http.Response) error {
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("%w: %s", ErrForgeStatus, resp.Status)
}

type refObject struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

func (c *Client) refSHA(ctx context.Context, ref string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/git/ref/heads/%s", c.Repo, ref), nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return "", statusError(resp)
	}
	var body refObject
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("github: decode ref %s: %w", ref, err)
	}
	return body.Object.SHA, nil
}

func (c *Client) createRef(ctx context.Context, branch, sha string) error {
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/git/refs", c.Repo), map[string]string{
		"ref": "refs/heads/" + branch,
		"sha": sha,
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return statusError(resp)
	}
	return nil
}

func (c *Client) deleteRef(ctx context.Context, branch string) error {
	resp, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/git/refs/heads/%s", c.Repo, branch), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return statusError(resp)
	}
	return nil
}

type contentsResponse struct {
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

// blobAt returns the decoded content and blob sha of path on ref. A path
// absent from ref (the ordinary state for a release cutting a file for the
// first time) reports ErrNotFound rather than an error the caller must
// special-case.
func (c *Client) blobAt(ctx context.Context, path, ref string) ([]byte, string, error) {
	u := url.URL{Path: fmt.Sprintf("/repos/%s/contents/%s", c.Repo, path), RawQuery: url.Values{"ref": {ref}}.Encode()}
	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, "", statusError(resp)
	}
	var body contentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", fmt.Errorf("github: decode contents %s: %w", path, err)
	}
	// GitHub wraps base64 content at 60 columns.
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body.Content, "\n", ""))
	if err != nil {
		return nil, "", fmt.Errorf("github: decode base64 %s: %w", path, err)
	}
	return content, body.SHA, nil
}

func (c *Client) putContents(ctx context.Context, path, branch string, content []byte, sha string) error {
	req := map[string]string{
		"message": "thump: " + branch,
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  branch,
	}
	if sha != "" {
		req["sha"] = sha
	}
	resp, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/contents/%s", c.Repo, path), req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return statusError(resp)
	}
	return nil
}

type pull struct {
	Number   int     `json:"number"`
	State    string  `json:"state"`
	MergedAt *string `json:"merged_at"`
	HTMLURL  string  `json:"html_url"`
}

// findPull returns the pull request for branch in the given state ("open"
// or "all"), or nil if none exists — absent is an ordinary outcome, not an
// error.
func (c *Client) findPull(ctx context.Context, branch, state string) (*pull, error) {
	u := url.URL{Path: fmt.Sprintf("/repos/%s/pulls", c.Repo), RawQuery: url.Values{
		"head":  {c.owner() + ":" + branch},
		"state": {state},
	}.Encode()}
	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, statusError(resp)
	}
	var pulls []pull
	if err := json.NewDecoder(resp.Body).Decode(&pulls); err != nil {
		return nil, fmt.Errorf("github: decode pulls for %s: %w", branch, err)
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return &pulls[0], nil
}

func (c *Client) createPull(ctx context.Context, branch, base, notes string) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls", c.Repo), map[string]string{
		"title": branch,
		"head":  branch,
		"base":  base,
		"body":  notes,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return "", statusError(resp)
	}
	var body pull
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("github: decode created pull for %s: %w", branch, err)
	}
	return body.HTMLURL, nil
}

func (c *Client) closePull(ctx context.Context, number int) error {
	resp, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/%d", c.Repo, number), map[string]string{
		"state": "closed",
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return statusError(resp)
	}
	return nil
}
