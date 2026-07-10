# AGENTS.md

Guidance for coding agents (and new contributors) working in this repository. This file
covers **Go conventions and testing standards only** — for project architecture, scope, and
history, see `CLAUDE.md` and the package docs under `internal/` and `api/`.

## Go house rules

- **Errors**: wrap with `%w`, compare with `errors.Is` / `errors.As`, combine with
  `errors.Join`. Use package-level `var ErrFoo = errors.New(...)` for sentinels.
- **Never return a typed-nil pointer as an `error`** — return literal `nil`.
- **Accept interfaces, return structs.** Interfaces are consumer-defined, not shipped
  alongside their implementation.
- **`context.Context` is always the first parameter**, never a struct field. Thread it
  through call chains; don't reach for `context.Background()` deep inside one.
- **Concurrency**: run tests with `-race`. Use `testing/synctest` (`synctest.Test`) for
  deterministic time/concurrency tests instead of real sleeps.
- **Benchmarks**: use `testing.B` and compare with `benchstat` before/after a change. Check
  escape analysis with `go build -gcflags=-m` when allocation matters.
- **Prefer stdlib**: `any` (not `interface{}`), builtins (`min`/`max`/`clear`), `log/slog`,
  `slices`/`maps` over hand-rolled loops.
- **Don't guess signatures or find-replace blindly** — use `go doc` or gopls/LSP tooling
  (e.g. rename-symbol) to change an API across call sites.

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
