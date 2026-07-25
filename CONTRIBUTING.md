# Contributing to thump

Help is welcome. This is a learning project as much as a build — the author is
using it to get fluent in Go — so the bar is "did we both understand why this is
right," not just "does it pass."

Read `README.md` first for what thump is. This file is about how to work here.

---

## The working agreement

- **The repo owner reviews and lands every commit.** Edits, tests, and
  `task ci` are fair game for anyone helping out (including an AI pairing
  partner); the commit itself is always the owner's to make. Open a PR, or hand
  over a diff.
- **`task ci` green is the definition of done.** That chain is
  fmt-check → vet → **lint** → vulncheck → chart-lint → race → build. Lint is
  GitHub's gate too, so `go test` passing is *not* the same claim — a red lint
  with green tests has silently broken `main` here before.
- **Red→green is the default shape, held loosely.** Write the test for new
  behavior first and watch it fail for the reason you predicted. It's a spine to
  work from, not a ritual to enforce on every change; sometimes a spike comes
  first, and that's fine.
- **Go conventions live in `AGENTS.md`** — error wrapping, doc-comment voice,
  ACE-style table-test names, `cmp.Diff(want, got)` argument order. Read it
  before touching any `.go` file. It is not a style suggestion; it's what makes
  a diff match what's already on disk.

## What gets reviewed hardest

Not bugs in business logic. These:

**1 · A catalog change is an execution-surface change.** `config/actions/catalog.yaml`
is the autonomy boundary *and*, since each action carries an `execution` block,
the binding to a real cluster mutation. The `exec` verb takes argv, so whoever
can merge that file can run a command in any pod thump's ServiceAccount can
reach. What bounds it is RBAC (`pods/exec`, scoped per namespace), the global
kill switch, and hiss's policy — **not** the verb list. So a PR touching the
catalog gets read like a PR touching an executor, whatever else it does.

**2 · The seams.** Each beat has a single job and a **never**-clause, and the
never-clauses *are* the architecture:

| Beat | Job | Never |
|---|---|---|
| `rattle` | represent reality | **never interprets** — facts only |
| `clank` | structured truth → candidate actions | **never acts** — proposals only |
| `hiss` | permission evaluation | **never re-reasons** — verdicts only |
| `thump` | approved actions → outcomes | **never decides** — contracts only |
| `click` | outcomes feed the next cycle | **never a module** — it's a return edge |

The regressions that matter most are the ones that erode a never-clause:

- a policy check — a confidence threshold, a freeze window — appearing anywhere
  but hiss (it becomes invisible and unauditable there);
- a raw payload riding in a conversation message instead of an `EvidenceRef`
  digest;
- a recomputed fingerprint, or a dedupe keyed on transport metadata (a filename,
  a sequence number) instead of the producer-assigned fingerprint;
- a new noun that isn't already in the vocabulary — every piece of state should
  point to exactly one boundary object.

`docs/invariants.md` has the full list and the four-question smell test.

**3 · Domain knowledge leaking into the engine.** No Ceph, flagd, cart, or
OpenTelemetry-specific branch belongs in engine code. App knowledge goes in
per-site config or the signal surface. The onboarding test's fixture domain is
deliberately named `acme` for exactly this reason: the day onboarding needs a
domain-specific Go discriminator, `test/onboarding/` is where it shows up.

**4 · Honest absence.** A missing measurement is `nil`, rendered `unmeasured` —
never `0` sitting next to a real value looking like a clean result. Declining
out loud beats guessing quietly, and that applies to this project's own status
page as much as its runtime.

## Getting set up

Build tooling is [go-task](https://taskfile.dev); `task --list-all` for
everything.

```sh
task ci                  # the gate: fmt-check, vet, lint, vulncheck, chart-lint, race, build
task test                # go test ./...
go test ./internal/clank -run TestGate -v
go test ./test/onboarding -v      # the whole engine, over a domain authored in config
```

`task ci` also needs `golangci-lint` and `helm` on PATH; everything else it
fetches with `go run`.

Test names here are written as full sentences on purpose, so the suite reads back
as a spec. `gotestdox ./...` is the tool for that, but be warned: as of Go 1.26
it exits 0 and prints **nothing** rather than reporting that it couldn't parse
`go test -json`. Plain `go test -v` is the reliable way to read names today.

No cluster and no API key are needed for any of that. `task eval` drives a real
model and is key-gated (`ANTHROPIC_API_KEY`) — it is deliberately *not* part of
`task ci`, because a real-model assertion is exactly the flakiness the gate
exists to keep out. A missing key is a clean skip, not a failure.

The five beats also run standalone (`task run:clank`, `run:rattle`, `run:hiss`,
`run:thump`, `run:trim`) and against a live cluster through Tilt — see
`README.md` § Standing it up locally. **Dry-run is the default and you have to
opt into anything else**; going live additionally requires an armed kill switch.

## A good first change

Adding to the action catalog. Author an action in `config/actions/catalog.yaml`
with an `execution` block, make sure `config/hiss/policy.yaml` has a confidence
floor for its (tier, failure-class) pair, and run `task ci`. No Go edit should be
needed — if one is, either you've found a genuinely new *kind* of mutation (which
needs a new mechanism in `internal/actuate` plus a test, and that's a real
contribution) or a bug worth reporting. Read the ⚠️ in point 1 above first.

## Commit messages

Explain the *why* — what was wrong or missing, and why this is the right shape.
The subject line is `type(scope): summary` (`feat(clank):`, `fix(thump):`,
`test(actuate):`, `docs:`). Please don't add AI-assistant attribution trailers
or footers; the author credits agent use in prose, on their own terms.
