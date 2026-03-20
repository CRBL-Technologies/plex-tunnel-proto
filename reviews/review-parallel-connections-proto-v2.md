# Code Review: claude/parallel-connections-proto-v2

**Branch:** `claude/parallel-connections-proto-v2`
**Compared to:** `main`
**Reviewer:** Claude
**Last updated:** 2026-03-20 (round 1)

---

## Summary

Implements the Phase 1 proto changes for protocol v2 parallel connections. Bumps
`ProtocolVersion` to `2`, adds `SessionID` and `MaxConnections` to `Message`,
updates `Validate()` with version-gated checks for the new fields, and updates
the frame serialisation, fuzz corpus, and test suite to match.

---

## What Was Done

- `message.go`: `ProtocolVersion` constant bumped `1 → 2`.
- `message.go`: `SessionID string` and `MaxConnections int` added to `Message`
  with `omitempty` JSON tags and inline doc comments.
- `message.go`: `Validate()` extended — `MsgRegister` requires
  `MaxConnections >= 1` when `ProtocolVersion == 2`; `MsgRegisterAck` requires
  both `SessionID` non-empty and `MaxConnections >= 1` when
  `ProtocolVersion == 2`.
- `frame.go`: `SessionID` and `MaxConnections` added to `frameMetadata` and
  threaded through `encodeFrameMetadata` / `decodeFrameMetadata`.
- `frame_test.go`: Round-trip test added for `RegisterAck` with session fields.
- `fuzz_test.go`: Seed corpus updated with new fields; `MsgRegisterAck` added
  as a fuzz seed (previously absent).
- `tunnel_test.go`: Validate test table extended with new passing and failing
  cases for both `MsgRegister` and `MsgRegisterAck`, plus two "legacy version
  shape still valid" cases that confirm `ProtocolVersion == 1` passes without
  session fields.

---

## What Looked Good

- **Version-gated validation is correct.** Using `m.ProtocolVersion ==
  ProtocolVersion` (the constant, now `2`) to gate the new field checks means
  messages with `ProtocolVersion: 1` still satisfy `Validate()` for their basic
  shape, letting `handleTunnel` return a clean protocol-mismatch error rather
  than a confusing field-validation error.

- **"Legacy version shape still valid" test cases.** These two cases pin the
  intended behaviour explicitly: a v1 register or register-ack without session
  fields must not fail `Validate()`. Good defensive test to have before the
  server and client are updated.

- **`omitempty` on both new fields.** Correct — `MaxConnections: 0` is omitted
  on the wire for v1 messages and is invalid for v2, so the zero value is never
  a legitimate encoded value.

- **Fuzz corpus completeness.** Adding `MsgRegisterAck` as a seed closes a gap
  that existed in the previous corpus (only `MsgRegister` was seeded for the
  handshake path).

---

## Issues

### Issue 1 — `Validate()` ties field checks to the constant, not a range (observation)

> `tunnel/message.go:60,68`

The checks use `m.ProtocolVersion == ProtocolVersion` (hardcoded to `2`). This
is fine for now, but if a future version 3 ever adds more required fields, a
`MsgRegister` with `ProtocolVersion == 3` would silently skip the v2 field
checks inside `Validate()`. Not a problem today — just worth noting for whoever
writes the v3 validation so they don't forget to update these guards.

No change needed in this PR.

---

## Test Results

```
ok  github.com/CRBL-Technologies/plex-tunnel-proto/tunnel  10.057s
```

All tests pass. No failures.

---

## Acceptance Criteria Checklist

From the Phase 1 TODO in `plex-tunnel` specs:

- [x] Add `SessionID string` and `MaxConnections int` to `Message`, both `omitempty`.
- [x] `Validate()`: `MsgRegister` with `ProtocolVersion == 2` requires `MaxConnections >= 1`.
- [x] `Validate()`: `MsgRegisterAck` with `ProtocolVersion == 2` requires `SessionID` non-empty and `MaxConnections >= 1`.
- [x] `Validate()` scoped correctly — unsupported protocol versions pass base shape checks.
- [x] `ProtocolVersion` bumped to `2`.
- [ ] Tag and release (e.g. `v1.1.0`) — to be done after server and client PRs are ready to consume.

---

## Verdict

**Approved.** Implementation is correct, tests pass, and the version-gating
approach in `Validate()` matches the spec. The release tag can wait until the
server and client PRs are ready to consume the new version.
