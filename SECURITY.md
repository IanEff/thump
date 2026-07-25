# Security Policy

## Supported versions

thump is pre-release and unversioned — there is no tagged release, and
therefore no supported-version matrix. Every report is evaluated against the
tip of `main`, because that's the only thing that exists to report against.
This will grow a real table the day a `v0.1.0` ships.

## Reporting a vulnerability

Please don't open a public issue for a suspected vulnerability — a public
issue is a disclosure, not a report. Use GitHub's private vulnerability
reporting instead: **Security tab → "Report a vulnerability"** on this
repository. That opens a private advisory thread with the maintainer rather
than a public inbox to babysit.

This is a single-maintainer project — expect an initial response in a few
days, not an SLA.

## Why the catalog is the sharpest edge here

thump reasons about live incidents and can act on a real Kubernetes cluster,
so the interesting attack surface isn't the usual web-app list — it's the
config that decides what the engine is allowed to do.

**A change to `config/actions/catalog.yaml` is a change to the execution
surface, not just to the reasoning it feeds.** Every action in that file
carries an `execution` block, and one verb (`exec`) takes argv directly —
`command: [ceph, osd, set, noout]`, authored plainly in YAML. Whoever can
merge a change to that file can run a command in any pod thump's
ServiceAccount can reach. There is no Go code standing between the catalog
and the cluster for that verb; the file *is* the binding.

**What bounds that isn't the verb list — it's RBAC.** thump's ServiceAccount
is scoped per namespace (see `deploy/chart/thump/templates/rbac-*.yaml`),
every live action additionally requires an armed global kill switch
(`internal/thump/killswitch.go`) and a passing verdict from thump's
governance stage, and a disarmed switch blocks execution regardless of what
the catalog or the governance verdict say. A compiled allowlist of
(namespace, selector) pairs for the `exec` verb was considered and
deliberately not built — RBAC is already the enforced bound, and a compiled
allowlist is the first thing worth adding the day this project starts taking
catalog changes from contributors who aren't already trusted reviewers.

In practice: a PR touching `config/actions/`, `config/hiss/`, or
`internal/actuate` gets reviewed as an execution-surface change, whatever
else it claims to do. See `CONTRIBUTING.md` § "What gets reviewed hardest"
for the reviewer's side of this same rule.

## Scope

In scope: this repository — `internal/`, `cmd/`, the Helm chart under
`deploy/`, and the authored catalog and policy config that ships with it.

Out of scope: the third-party services thump talks to (Prometheus, Loki,
NATS, the Anthropic API) — report vulnerabilities in those upstream, to their
own maintainers.
