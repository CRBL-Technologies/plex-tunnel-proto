# plex-tunnel-proto v2 module path (Go-semver-compat follow-up to #298 Phase A)

Full context: https://github.com/CRBL-Technologies/plex-tunnel-server/issues/298 (round-4-final body).

Base: `origin/main` (HEAD = `38b1ee2`).
Target PR: `chore/proto-v2-module-path` → `main`.
Tag: CEO will retag (delete v2.0.0, create v2.0.1 on merge commit) after merge — **do NOT tag yourself**.

## Context

PR #26 shipped the v4 wire additions and was tagged `v2.0.0`. The tag is not Go-module-consumable because the `go.mod` `module` directive still reads `github.com/CRBL-Technologies/plex-tunnel-proto` (no `/v2` suffix). Go's semver-import-path rule requires any module at v2+ to carry the major version in the import path — `go get ...@v2.0.0` fails with:

```
invalid version: module contains a go.mod file, so module path must match
major version ("github.com/CRBL-Technologies/plex-tunnel-proto/v2")
```

This is a tiny follow-up to make the v2 tag consumable. The CEO will delete the existing v2.0.0 tag (no consumers yet, safe) and retag the merge commit as v2.0.1.

Phase B (plex-tunnel-server) and Phase C (plex-tunnel) will both rewrite their imports from `github.com/CRBL-Technologies/plex-tunnel-proto/tunnel` to `github.com/CRBL-Technologies/plex-tunnel-proto/v2/tunnel` in their own PRs; that is NOT part of this TASK.

## Goal

`go.mod` module path reads `github.com/CRBL-Technologies/plex-tunnel-proto/v2`. `go build`, `go test`, `go vet` stay green. No code logic changes.

## Constraints / guardrails

- **DO NOT delete or modify code not mentioned in this task.**
- Do NOT touch any `.go` files. Confirmed: `grep -rn 'plex-tunnel-proto' . --include='*.go'` returns zero hits, so there are no internal self-imports to rewrite. If you somehow find one, STOP and escalate.
- Do NOT change `go.sum` manually. If `go mod tidy` wants changes, let it run — but the expectation is zero sum changes for this rewrite since no dependencies moved.
- Do NOT tag `v2.0.1`.

## Tasks

### 1. `go.mod`

- [ ] Change line 1 from `module github.com/CRBL-Technologies/plex-tunnel-proto` to `module github.com/CRBL-Technologies/plex-tunnel-proto/v2`. Leave everything else in `go.mod` untouched.

### 2. Verify + tidy

- [ ] Run `go build ./...` — must succeed.
- [ ] Run `go test ./...` — must succeed.
- [ ] Run `go vet ./...` — must succeed.
- [ ] Run `go mod tidy` — then inspect `git diff go.mod go.sum`. No changes expected; if any, report in your completion message so the lead can review.

### 3. Optional doc update (safe, narrow)

- [ ] If `docs/wire-v4.md` references the module import path anywhere, update to `/v2/tunnel` form. (Likely not needed; check with `grep plex-tunnel-proto docs/`.)

## Acceptance criteria

- `go.mod` line 1 = `module github.com/CRBL-Technologies/plex-tunnel-proto/v2`.
- No `.go` files modified.
- `go build ./...`, `go test ./...`, `go vet ./...` all green.
- `go mod tidy` produces no further diff.
- Commit message: `chore(proto): rewrite module path to /v2 for Go-semver-compat`.

## Verification

```
cd /home/dev/worktrees/noah/plex-tunnel-proto
grep '^module' go.mod
go build ./...
go test ./...
go vet ./...
go mod tidy && git diff --exit-code go.mod go.sum
```

All should pass.

## Out of scope

- Tagging anything (CEO handles).
- Any downstream server/client import rewrites (separate Phase B / Phase C PRs).
- Any wire-format, message-type, capability, or validation changes.
