# plex-tunnel-proto v1.4.0 — fresh-register validator relaxation (#297)

Full spec: https://github.com/CRBL-Technologies/plex-tunnel-server/issues/297 (Proto v1.4.0 section).

Base: `origin/main` (proto uses main as the release target per gitflow).
Target PR: `fix/297-validator-relax` → `main`.
Tag: `v1.4.0` to be cut by CEO after merge (do NOT tag yourself).

## Context

The server proto validator currently rejects fresh register (`SessionID == ""`) when `MaxConnections < 1`. This forces clients to send a non-zero value even though the server ignores the wire field and derives the grant from the DB. The result: free-plan clients with no `PLEXTUNNEL_MAX_CONNECTIONS` env var fail silently at handshake. Part (a) of the issue-#297 motivation.

## Goal

Allow fresh register frames to carry `MaxConnections == 0` without validation error. Resume-register behavior (when `SessionID != ""`) is unchanged.

## Constraints / guardrails

- **DO NOT delete or modify code not mentioned in this task.**
- Do NOT touch resume-register validation, `RegisterAck` validation, or `MsgMaxConnectionsUpdate` validation.
- Do NOT bump `ProtocolVersion`. This is a purely permissive change; existing clients sending positive values continue to validate.
- Do NOT tag `v1.4.0` yourself; the CEO handles tags post-merge.

## Tasks

- [ ] **`tunnel/message.go` `Validate()`** (~lines 78-90): in the `case MsgRegister:` branch, delete the `if m.SessionID == "" && m.MaxConnections < 1 { return errors.New("register message missing or invalid max_connections") }` check entirely. Keep the `Token`, `ProtocolVersion`, and `ProtocolVersion < ProtocolVersion` checks intact.
- [ ] **`tunnel/tunnel_test.go` `TestMessageValidate`** table entries (~lines 23-28):
  - Flip `"register missing max connections"` from `wantErr: true` to success (remove the `wantErr: true`).
  - Flip `"register invalid max connections"` (the `MaxConnections: 0` case) from `wantErr: true` to success. Rename to `"register fresh ok with max connections 0"` for clarity. This serves as the explicit pin for the new behavior.
  - Retain `"register join ok without max connections"` unchanged (already passing with `SessionID: "sess-1"` + no `MaxConnections`).
  - Retain `"register ok"` unchanged.
- [ ] **Module version metadata:** if a `CHANGELOG.md` exists, append a v1.4.0 entry noting the validator relaxation. If no CHANGELOG exists, do not create one; the tag + GitHub release notes handle it.

## Tests

`go test ./...` must pass. In particular `TestMessageValidate` now asserts:
- Fresh register with `MaxConnections: 0` → no error.
- Fresh register with `MaxConnections: 4` → no error (positive values still work, backwards-compat).
- Join register with `SessionID` set and `MaxConnections: 0` → no error (unchanged).
- Register ack with `MaxConnections: 0` → still errors (unchanged; ack must always carry the granted count).

## Acceptance criteria

- [ ] `tunnel/message.go` `Validate()` MsgRegister branch no longer references `MaxConnections` at all.
- [ ] `tunnel/tunnel_test.go` `TestMessageValidate` includes a case named "register fresh ok with max connections 0" with `wantErr: false`.
- [ ] All existing tests still pass.
- [ ] No other file is modified.
- [ ] PR body links to issue #297, notes the v1.4.0 bump intent, and flags that the CEO tags post-merge.

## Verification

```bash
cd /home/dev/worktrees/kate/plex-tunnel-proto
go vet ./...
go test ./...
```
