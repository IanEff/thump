---
name: go-navigation
description: Use before searching or reading unfamiliar Go code in this repo — how to drive the gopls MCP tools instead of grep-first, and how to route around client-go/apimachinery noise in go_search.
---

# Navigating thump with gopls

At ~17.8kloc across five beat packages plus the `api/v1` wire leaves, grep finds a string
but misses the seam. Reach for these before Read/Grep on anything Go-shaped:

- `go_workspace` once per session to orient.
- `go_search` to locate a symbol by fuzzy name.
- `go_file_context` right after reading any Go file for the first time, to see what it
  pulls from the rest of its package.
- `go_symbol_references` / `go_rename_symbol` before touching a call site that crosses
  files.

## The `go_search` noise problem

`go_search` wraps gopls' `workspace/symbol`, governed by the `symbolScope` setting
(default `"all"` — workspace *and* every dependency *and* stdlib). Headless `gopls mcp`
(how this MCP server is launched) has no config surface to set `symbolScope: "workspace"`
— it always uses the hardcoded default (confirmed by reading `gopls@v0.21.1`'s
`internal/cmd/mcp.go`/`cmd.go`). A generic fuzzy query (`"gate"`, `"binding"`, `"action"`)
gets buried under `k8s.io/client-go` + `apimachinery`'s typed-client/lister/informer
surface before it ever reaches the 100-result cap.

Two mitigations, both cheap:

1. **Qualify the query with a path fragment unique to this repo** —
   `go_search("internal/clank Gate")` or `go_search("thump/internal/actuate Binding")`
   outranks `k8s.io/*` hits even though the match is still fuzzy, not a hard filter.
2. **When a query is clearly drowning anyway, drop straight to `Read`** on the file you
   already suspect rather than trying to out-fuzzy the dependency graph — that's not
   "grep-first," it's the correct fallback once search itself is the bottleneck.
