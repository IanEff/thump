# AGENTS.md

Guidance for coding agents (and new contributors) working in this repository. This file
covers **Go conventions, comment/doc-comment style, and testing standards only** — for
project architecture, scope, and history, see `CLAUDE.md` and the package docs under
`internal/` and `api/`.

## Go house rules

- **Errors**: wrap with `%w`; compare sentinels with `errors.Is`. To pull a typed error back
  out, prefer `errors.AsType[T](err) T` (Go 1.26+) over the `var target T; errors.As(err,
  &target)` two-step — fall back to `errors.As` only where a call site can't assume 1.26.
  Combine multiple errors with `errors.Join`. Package-level `var ErrFoo = errors.New(...)`
  for sentinels.
- **Never return a typed-nil pointer as an `error`** — return literal `nil`.
- **Accept interfaces, return structs.** Interfaces are consumer-defined, not shipped
  alongside their implementation.
- **`context.Context` is always the first parameter**, never a struct field. Thread it
  through call chains; don't reach for `context.Background()` deep inside one.
- **Concurrency**: run tests with `-race`. Launch a tracked goroutine with
  `wg.Go(func() { ... })` (`sync.WaitGroup.Go`, Go 1.25+) instead of hand-rolling `wg.Add(1);
  go func() { defer wg.Done(); ... }()`. Use `testing/synctest` (`synctest.Test`, GA since Go
  1.25) for deterministic time/concurrency tests instead of real sleeps.
- **Benchmarks**: use `testing.B` and compare with `benchstat` before/after a change. Check
  escape analysis with `go build -gcflags=-m` when allocation matters.
- **Prefer stdlib**: `any` (not `interface{}`), builtins (`min`/`max`/`clear`), `log/slog`,
  `slices`/`maps` over hand-rolled loops.
- **Don't guess signatures or find-replace blindly** — use `go doc` or gopls/LSP tooling
  (e.g. rename-symbol) to change an API across call sites.
- **Modernize deliberately, not on autopilot.** `go fix` (rebuilt in Go 1.26 as the home of
  Go's modernizers, on the same analysis framework as `go vet`) can bulk-rewrite old idioms
  to current ones. Run it as a proposal to review, not a pre-commit step — a mechanical
  rewrite across every package is still a diff a human should read before it lands.

## Comments and doc comments

Every exported identifier — package, type, func, const, var, and any struct field whose
meaning isn't obvious from its name and type — gets a doc comment. Two audiences read these
who were never in the room for the design conversation: `pkg.go.dev`, and a future
contributor mid-incident with no context loaded. Write for them.

### What belongs

- **Lead with the invariant, not the mechanism.** State what must always be true, not what
  the code happens to do this week.
  - Bad: `EvidenceRef holds a digest string and a backend reference.`
  - Good: `EvidenceRef carries a digest and a backend ref — never a Raw field, and never
    will; raw payloads cannot enter the conversation history.`
- **The boundary is as much the doc as the thing.** If a type's job is defined partly by
  what it refuses to do, say so. A reader should be able to tell what would be a violation,
  not just what's a feature.
- **Em-dash the reasoning onto the fact; don't subordinate-clause it.** One declarative
  sentence, then the "why" appended after a dash.
  - Bad: `GateResult represents the readiness state produced when a set of minimums are
    evaluated as a conjunction rather than averaged together.`
  - Good: `GateResult is a conjunction of minimums, never an average — one failing
    dimension fails the whole gate.`
- **Numbers over qualifiers.** If a const exists, cite it: `MaxSteps bounds the loop at 12
  turns` beats `the loop is bounded to prevent runaway execution` every time.
- **Struct fields get `value — reason`, not restated names.** `Threshold float64 // the
  per-tier confidence floor; read by Authority.Evaluate, never by the gate` — not
  `Threshold is the threshold value`.
- **Start at the invariant.** Go convention wants the comment to begin with the identifier
  name (`// Engine runs …`) — satisfy that mechanically, then get out of the way. Don't open
  with "Engine is a struct that implements the engine for…".

### What doesn't belong

- **No internal process jargon.** Workstream codenames, wave/stage numbers, PR numbers,
  ticket IDs, doc-internal shorthand ("the compat rule", "WS3 Stage 1") mean nothing to a
  reader who wasn't in that conversation. State the rule itself in plain language instead.
- **No task/caller narration.** Don't reference the change, fix, or caller that motivated the
  comment ("used by the X flow", "added for issue #123", "handles the case from the retry
  bug") — that belongs in the commit message or PR description, and rots the moment the
  caller changes.
- **No restating the identifier.** If removing the comment would lose no information a
  competent reader didn't already have from the name and type, delete it.
- **No teaching Go or general programming.** A doc comment is not the place to explain what a
  goroutine, interface, channel, or `context.Context` is, or to restate language semantics
  ("this uses a mutex to prevent concurrent access"). Comments target a competent Go reader —
  explaining the language itself belongs in conversation, not shipped code.
- **No hedging, no marketing adjectives.** Banned on sight: robust, powerful, elegant,
  seamless, simply, flexible. Name the trade-off instead of softening it — "fail the sync
  rather than poll forever," not "gracefully handles timeout scenarios."
- **No multi-paragraph docstrings or comment blocks.** One or two sentences. A third
  paragraph is design documentation, and belongs wherever this project keeps that — not the
  source file.

### Litmus test

If the sentence could appear verbatim in a generic SaaS onboarding doc, it's not done — keep
rewriting until it could only be true of this codebase.

**Worked example:**

```go
// Bad — generic, could be any project:
// Package clank implements the reasoning engine for the thump system. It
// provides a flexible and powerful framework for processing signals and
// generating proposals using an LLM-based approach.

// Good — states the invariant and the boundary:
// Package clank is the Reasoning Plane: a bounded LLM loop that turns one
// rattle SignalDetection into a ranked, evidence-backed ProposalSet. It
// selects; it never permits — authority lives in hiss, not here. The
// ActionContract catalog is the autonomy boundary: nothing outside it can
// be proposed or executed.
```

## Testing standards

Tests are a communication medium whose primary channel is *failure* — a test should read as
a falsifiable claim, and its failure message should tell you what broke without opening the
source.

### 1. Design for testability

- **Interface seams over concrete dependencies.** Accept `io.Reader`/`io.Writer` instead of
  `*os.File`; satisfy them in tests with `strings.NewReader(...)`, `io.Discard`, or
  `bytes.Buffer`.
- **Filesystem abstraction.** Code that walks a filesystem should accept `fs.FS` — `os.DirFS`
  in production, `fstest.MapFS` in tests for a fast, zero-I/O, deterministic fake.
- **Pure functions first.** Pull complex logic into pure functions before wiring in
  side effects — purity is what makes table testing trivial.

### 2. Table-driven tests and ACE names

- **Map-based tables** (`map[string]struct{...}`) over slice-based tables — a map's
  non-deterministic iteration order is a free detector for order-dependent bugs.
- **ACE names.** Each table key (and thus each `t.Run` subtest name) is a full **Action,
  Condition, Expectation** sentence, e.g. `"FormatProposal returns valid JSON for empty
  input"`. If the name isn't a complete sentence, it's missing context. `gotestdox ./...`
  should read the suite back as a spec.
- **One behavior per test.** A function can have many behaviors; a test asserts one claim.
- **No `tc := tc` capture boilerplate** — Go 1.22+ loop variables are per-iteration; don't
  reintroduce the old workaround.

### 3. Assertions and failure messages

- **Use `cmp.Diff(want, got)`** (`github.com/google/go-cmp/cmp`) instead of
  `t.Errorf("want %v, got %v", want, got)`. Argument order is always `(want, got)` — `want`
  produces `-` lines, `got` produces `+` lines; flipping it makes every diff read backward.
  You'll see this called "the Arundel Standard" in some test comments (e.g.
  `internal/clank/metrics_tool_test.go`) — same rule, informal name.
- **Name the user-visible failure**, then attach the diff: `t.Error("wrong generated
  proposal", cmp.Diff(want, got))` — not a bare diff with no context.
- **Unexpected errors are fatal.** If an `err != nil` would invalidate `got`, call
  `t.Fatal(err)` immediately rather than `t.Error`, or a later nil-pointer access panics the
  test.

### 4. Error testing is mandatory

- **Never discard an error with `_`** in a test unless it's the one specific line checking a
  want-error case.
- **Want-error and want-success are separate paths**: for an expected error, ignore the
  success value (`_, err := ...`) and assert the error; for expected success, assert
  `err == nil` via `t.Fatal`, then compare results.
- **Assert sentinels with `errors.Is(err, expectedErr)`**, never string-matching an error
  message. Production code should wrap errors with `%w` for context.

### 5. Test data design

- **Equivalence classes.** Partition inputs and test the boundaries (zero, empty,
  max-length, negative, nil) rather than exhaustively covering one class.
- **Dummy values.** Use explicitly fake data (e.g. `"dummy token"`) for a parameter that
  shouldn't affect the behavior under test — it documents the contract.
- **No shared package-level `var` maps/slices as test fixtures.** Provide a constructor
  (`makeTestData()`) so each test gets its own independent allocation.
- **Golden files** are useful for large struct outputs (`os.ReadFile("testdata/x.golden")`).
  Never add an `-update` flag that rewrites goldens automatically — updates are hand-rolled
  and deliberately reviewed.

### 6. The red-green loop

Write the test for new behavior first; see it fail for the *specific* reason predicted (not
a panic or compile error); write the minimum code to pass it; refactor under a green suite.
This is a spine to work from, not a ritual to enforce on every change.
