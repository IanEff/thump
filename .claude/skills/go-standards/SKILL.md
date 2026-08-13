---
name: go-standards
description: Use before writing or editing any Go code, comments, doc comments, or tests in this repo — loads AGENTS.md's house rules first.
---

# Go standards for thump

`AGENTS.md` at the repo root is the canonical spec for Go conventions here — house
rules, comment/doc-comment voice, and testing standards. It is not optional background
reading; it is the thing that makes generated code, docs, and tests match what's
already on disk.

**Before writing or editing any `.go` file — code, a doc comment, a struct field
comment, or a test — read `AGENTS.md` in full if it isn't already loaded this
session.** Skimming this skill instead of the file is not enough; AGENTS.md has the
actual rules (error wrapping, `errors.AsType[T]`, ACE table-test names, `cmp.Diff`
argument order, the comment voice worked example) and they change independently of
this pointer.

Quick recall of the traps that most often get missed without a re-read:

- Doc comments state the invariant and the boundary, not the mechanism — see AGENTS.md
  § Comments and doc comments for the worked before/after example. No wave/stage/PR
  numbers or task narration in shipped comments.
- Table tests are map-based with full ACE (Action, Condition, Expectation) sentence
  names, not slice-based with short labels.
- Assertions use `cmp.Diff(want, got)` — `want` first, always — not `%v` string
  formatting.
- `errors.AsType[T](err) T` (Go 1.26+) is preferred over the `var target T; errors.As`
  two-step.

If AGENTS.md and this skill ever disagree, AGENTS.md wins — update this skill's recall
list to match, don't route around the file.
