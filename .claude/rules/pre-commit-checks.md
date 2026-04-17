---
paths:
  - "**/*.go"
  - "go.mod"
  - "go.sum"
---

# Pre-commit checks for Go changes

Before creating a commit that touches `*.go`, `go.mod`, or `go.sum`, run the repo's lint + test targets. `go build` and `go test` alone do NOT catch gofmt/goimports/golangci-lint issues, and CI's `golangci-lint` will fail on them.

```
make fmt      # gofmt -s -w . && goimports -w . (if installed)
make lint     # golangci-lint run — catches gofmt, unused, etc.
make test-race
```

**Why:** CI has failed repeatedly on trailing blank lines and formatting issues that `go build`/`go test` let through. `make lint` is the authoritative local mirror of the CI gate.

**How to apply:**
- Always run `make fmt` first — it rewrites files in place, so stage *after* it runs.
- Then `make lint` to confirm nothing else is flagged.
- `make test-race` is the pre-commit default per the Makefile (line 21).
- Only create the commit once all three are clean.
- If `golangci-lint` isn't installed locally, at least run `gofmt -l .` and `go vet ./...` — they catch the common cases.
