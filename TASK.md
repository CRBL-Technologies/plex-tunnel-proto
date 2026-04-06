# TASK: ECC proto batch 1 — downgrade gap + 32-bit overflow window

## Context

A fresh post-round-2 ECC audit (2026-04-06) flagged two direct gaps in the
recently-claimed P0 hardening of `plex-tunnel-proto`. Both are in the tunnel
package and both are security-critical. This batch covers **exactly these two
findings and nothing else** — other audit findings (proto `SendContext`, client
proto bump, server webhook idempotency, etc.) are separate later batches.

## Goal

Close two direct P0 gaps in the shared wire protocol:

1. `MsgRegisterAck` validation must reject downgraded protocol versions, not
   just zero — symmetric with the existing `MsgRegister` check.
2. `maxFrameComponentSize` must be tight enough that
   `frameHeaderSize + headerLen + bodyLen` cannot overflow `int` on 32-bit
   GOARCH (where `int` is `int32`).

## Constraints / guardrails

**DO NOT delete or modify any existing code not explicitly mentioned in this
task.** This includes:

- **DO NOT** bump `ProtocolVersion` in `tunnel/message.go`. It stays at `2`.
- **DO NOT** touch any file other than:
  - `tunnel/message.go`
  - `tunnel/message_test.go`
  - `tunnel/frame.go`
  - `tunnel/frame_test.go`
- **DO NOT** drive-by refactor unrelated code, rename fields, reshuffle
  imports, re-flow comments, or reorder existing test functions.
- **DO NOT** change the wire format, the frame header layout, or the
  `maxMetadataSize` / `maxHeaderEntries` constants.
- **DO NOT** touch `tunnel/websocket.go`, `tunnel/transport.go`,
  `tunnel/fuzz_test.go`, `tunnel/tunnel_test.go`, `tunnel/benchmark_test.go`,
  or anything in the repo root.
- **DO NOT** address any other finding from any review. Scope is exactly the
  two fixes below. If you notice something else, leave it alone.

## Tasks

### 1. `tunnel/message.go` — `MsgRegisterAck` downgrade reject (Critical)

In `Message.Validate()` the `MsgRegisterAck` branch (currently around
`message.go:75-87`) only rejects `ProtocolVersion == 0`. It must also reject
any `ProtocolVersion < ProtocolVersion`, mirroring the `MsgRegister` branch at
`message.go:69-71`.

- [ ] In the `case MsgRegisterAck:` branch, after the existing
  `m.ProtocolVersion == 0` check, add a second check:

  ```go
  if m.ProtocolVersion < ProtocolVersion {
      return fmt.Errorf("unsupported protocol version %d (minimum %d)", m.ProtocolVersion, ProtocolVersion)
  }
  ```

  Match the exact error-message format already used in the `MsgRegister`
  branch so the wording is identical across directions.

- [ ] Do **not** reorder or rewrite any other existing check in the switch.
  Do **not** touch the `MsgRegister` branch itself (it is already correct).
  Do **not** add any upper-bound check on `ProtocolVersion` — that is a
  separate Low finding and out of scope for this batch.

### 2. `tunnel/frame.go` — tighten `maxFrameComponentSize` to prevent 32-bit overflow (High)

`maxFrameComponentSize = 1<<30 - 1` (~1 GB) allows
`frameHeaderSize + headerLen + bodyLen` to reach `2^31 + 7`, which overflows
`int32` on 32-bit GOARCH. The fix is to tighten the constant so that two
maximum components plus the 9-byte frame header cannot exceed `math.MaxInt32`.

- [ ] Change the `maxFrameComponentSize` declaration (currently
  `frame.go:99-103`) to:

  ```go
  // maxFrameComponentSize limits individual header/body lengths so that
  // frameHeaderSize + headerLen + bodyLen cannot overflow int on 32-bit
  // GOARCH (where int is int32). Two components at this cap plus the 9-byte
  // frame header stay within math.MaxInt32.
  const maxFrameComponentSize = (math.MaxInt32 - frameHeaderSize) / 2
  ```

  `math` is already imported in this file, so no import change is needed.

- [ ] Do **not** change the existing `UnmarshalFrame` range check logic at
  `frame.go:113-114`. The existing bounds check continues to use
  `maxFrameComponentSize`; the only change is that the constant itself
  becomes tighter.

- [ ] Do **not** change `maxMetadataSize`, `maxHeaderEntries`, the wire
  format, or `NewFrame` / `MarshalBinary` behavior.

- [ ] The value of `maxFrameComponentSize` after this change will be
  `(math.MaxInt32 - 9) / 2 = 1073741819` — still roughly 1 GiB, so no
  legitimate receiver behavior changes. If your computation says otherwise,
  stop and re-check before proceeding.

## Tests

Add focused tests. Do not modify existing test functions.

### In `tunnel/message_test.go`

- [ ] **`TestMsgRegisterAckValidateProtocolVersion`** — table-driven test
  covering at minimum:
  - valid: `ProtocolVersion = ProtocolVersion` (current, i.e. `2`) — must
    return nil when subdomain/session/max-connections are also set
  - invalid: `ProtocolVersion = 0` — must return error (pre-existing
    behavior, keep covered)
  - invalid: `ProtocolVersion = 1` — must return error. This is the
    regression case: before this fix, a v1 ack after a v2 register would be
    accepted. Assert `err != nil`.
  - invalid: `ProtocolVersion = ProtocolVersion - 1` — symbolic form of
    the above; guards against a future `ProtocolVersion` bump silently
    re-opening the downgrade hole.

  In every "valid" row, all other required `MsgRegisterAck` fields
  (`Subdomain`, `SessionID`, `MaxConnections`) must be set so the test is
  isolated to the version check.

### In `tunnel/frame_test.go`

- [ ] **`TestMaxFrameComponentSizeBounds`** — a plain unit test (no
  sub-tests needed) that asserts the arithmetic invariant:

  ```go
  // Two max-size components plus the frame header must not overflow int32.
  sum := int64(frameHeaderSize) + 2*int64(maxFrameComponentSize)
  if sum > int64(math.MaxInt32) {
      t.Fatalf("maxFrameComponentSize too large: 2*%d + %d = %d exceeds math.MaxInt32 (%d)",
          maxFrameComponentSize, frameHeaderSize, sum, math.MaxInt32)
  }
  ```

  Use `int64` for the arithmetic in the test itself so the test is valid on
  both 32-bit and 64-bit GOARCH. Import `math` in the test file if it is not
  already imported.

- [ ] **`TestUnmarshalFrameRejectsOversizeComponent`** — construct a 9-byte
  wire header declaring `headerLen = maxFrameComponentSize + 1` and
  `bodyLen = 0`, pass it through `UnmarshalFrame`, and assert a non-nil
  error. This pins the existing bounds-check behavior against future
  regression. (If an equivalent test already exists under a different name,
  leave it alone and skip this bullet — do not duplicate.)

Do not add fuzz targets, benchmarks, or integration tests. Unit tests only.

## Acceptance criteria

- `tunnel/message.go` `MsgRegisterAck` branch rejects `ProtocolVersion < ProtocolVersion` with the same error format as `MsgRegister`.
- `tunnel/frame.go` `maxFrameComponentSize` equals `(math.MaxInt32 - frameHeaderSize) / 2` and the comment above it explains the 32-bit overflow rationale.
- New tests `TestMsgRegisterAckValidateProtocolVersion`, `TestMaxFrameComponentSizeBounds`, and (if not already present) `TestUnmarshalFrameRejectsOversizeComponent` exist and pass.
- All previously existing tests still pass without modification.
- `git diff` touches only `tunnel/message.go`, `tunnel/message_test.go`, `tunnel/frame.go`, `tunnel/frame_test.go`.
- No changes to `ProtocolVersion`, wire format, or any other file in the repo.

## Verification

From the worktree root:

```bash
go build ./...
go vet ./...
go test ./...
```

All three must pass clean. Commit the changes with a descriptive message
(e.g. `fix(proto): reject downgraded MsgRegisterAck and harden 32-bit frame size bound`)
and stop — do not push, the lead will review the diff first.
