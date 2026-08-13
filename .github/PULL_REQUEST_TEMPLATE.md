## Checklist

- [ ] `task ci` is green locally (fmt-check, vet, lint, vulncheck, chart-lint, race, build) — GitHub doesn't run `race`, so this is the only place it's checked.
- [ ] This does **not** touch a beat's never-clause (see `README.md` § Invariants / `CONTRIBUTING.md` § "What gets reviewed hardest") — or, if it does, that's called out below.
- [ ] This does **not** change the execution surface (`config/*/actions/catalog.yaml`, `config/*/hiss/policy.yaml`, `internal/actuate`) — or, if it does, that's called out below.
