# TASK: ECC proto batch 2 — SendContext w/ ctx-aware write lock

## Context

The 2026-04-06 ECC audit (`/home/dev/.claude-admin/reports/2026-04-06-ecc-review-portainer.md`) flagged
`tunnel/websocket.go:184` (High):

> **`Send` is not context-cancellable** — Hardcoded `context.Background()` while
> holding `writeMu`; one slow peer stalls all writers up to 30s with no abort.
> Add `SendContext`.

This is batch 2 in the fix order. Batch 1 (ack-side downgrade + 32-bit overflow)
already merged to `dev`. Batch 3 (client bumps proto + uses SendContext) is
gated on this batch shipping and a proto release tag.

The architecture call has been made with the CTO:

- **Full SendContext**, not minimum-scope. The finding explicitly says "all
  writers" — meaning waiters queued on the write lock must also abort on
  `ctx.Done`, not just the currently-writing goroutine.
- To do that, `writeMu sync.Mutex` is replaced with a 1-buffered
  `chan struct{}` acting as a ctx-aware binary semaphore. Plus a `closed`
  channel (closed by `Close()`) so waiters also unblock on connection teardown.
- **Public API stays backwards-compatible.** `Send` and `SendWithTiming` keep
  working unchanged from a caller's perspective — they internally delegate to
  the same code path with `context.Background()`.
- **`SendContextWithTiming` is explicitly out of scope** for this batch. If
  anyone later wants ctx+timing together, that's a new batch.

## Goal

Add ctx-aware sending to `WebSocketConnection` so that callers can cancel a
blocked or queued send via `context.Done()`, across both lock-wait and
WebSocket-write phases, while preserving the existing `Send` /
`SendWithTiming` API.

## Constraints / guardrails

**DO NOT delete or modify any existing code not explicitly mentioned in this
task.** This includes:

- **DO NOT** touch any file other than:
  - `tunnel/websocket.go`
  - `tunnel/tunnel_test.go`
- **DO NOT** touch `Receive` / `ReceiveContext` or any code path unrelated to
  sending.
- **DO NOT** bump `ProtocolVersion` or change any wire format.
- **DO NOT** change the existing public signatures of `Send(msg Message) error`
  or `SendWithTiming(msg Message) (SendTiming, error)`. Callers must keep
  compiling and behaving identically (same error paths, same SendTiming
  semantics, same blocking behavior when called without a cancellable ctx).
- **DO NOT** add `SendContextWithTiming` — explicitly out of scope. If you feel
  the urge, stop and leave it alone.
- **DO NOT** add new exported types, new exported fields, or new exported
  constants beyond `SendContext`.
- **DO NOT** refactor `SendTiming`, the timing measurement, or the
  `FrameEncode` / `WriteLockWait` / `WebSocketWrite` split. The new code must
  preserve the same timing behavior for `SendWithTiming`.
- **DO NOT** add external dependencies. `golang.org/x/sync/semaphore` is not
  allowed. Use plain channels.
- **DO NOT** touch any other ECC finding. Scope is exactly this one finding.

## Tasks

### 1. Replace `writeMu sync.Mutex` with a ctx-aware channel lock

Inside `tunnel/websocket.go`, in the `WebSocketConnection` struct (currently
`websocket.go:149-157`):

- [ ] Replace the `writeMu sync.Mutex` field with:

  ```go
  // writeLock is a 1-buffered channel acting as a ctx-aware binary semaphore.
  // Acquisition selects on ctx.Done and closed, so waiters can abort on
  // context cancellation or connection teardown instead of blocking on an
  // uninterruptible Mutex.Lock.
  writeLock chan struct{}
  // closed is closed exactly once by Close so in-flight and queued
  // SendContext callers unblock instead of waiting up to writeTimeout.
  closed    chan struct{}
  closeOnce sync.Once
  ```

  Keep `Encrypted`, `lastActivity`, `conn`, `remoteAddr`, `readTimeout`,
  `writeTimeout` exactly as they are. Preserve field order visually where
  reasonable; the only net changes are: removed `writeMu`, added `writeLock`,
  `closed`, `closeOnce`. The `sync` import remains needed for `sync.Once`.

- [ ] In `newWebSocketConnection` (currently `websocket.go:169-182`), initialize
  the two new channels alongside the existing assignments:

  ```go
  writeLock: make(chan struct{}, 1),
  closed:    make(chan struct{}),
  ```

### 2. Introduce the internal workhorse `sendContextWithTiming`

Add a new unexported method (placement: immediately above the existing `Send`
at `websocket.go:184`, or wherever reads cleanly — keep it in the obvious spot):

```go
// sendContextWithTiming is the internal workhorse for all send paths.
// ctx governs both lock acquisition and the underlying WebSocket write.
// A nil ctx is treated as context.Background() — but public callers should
// always pass a concrete context.
func (c *WebSocketConnection) sendContextWithTiming(ctx context.Context, msg Message) (SendTiming, error) {
    var timing SendTiming
    if ctx == nil {
        ctx = context.Background()
    }

    lockWaitStartedAt := time.Now()
    select {
    case c.writeLock <- struct{}{}:
        timing.WriteLockWait = time.Since(lockWaitStartedAt)
    case <-ctx.Done():
        timing.WriteLockWait = time.Since(lockWaitStartedAt)
        return timing, fmt.Errorf("send websocket message: %w", ctx.Err())
    case <-c.closed:
        timing.WriteLockWait = time.Since(lockWaitStartedAt)
        return timing, fmt.Errorf("send websocket message: %w", net.ErrClosed)
    }
    defer func() { <-c.writeLock }()

    frameEncodeStartedAt := time.Now()
    frame, err := NewFrame(msg)
    if err != nil {
        timing.FrameEncode = time.Since(frameEncodeStartedAt)
        return timing, fmt.Errorf("build websocket frame: %w", err)
    }
    payload, err := frame.MarshalBinary()
    if err != nil {
        timing.FrameEncode = time.Since(frameEncodeStartedAt)
        return timing, fmt.Errorf("encode websocket frame: %w", err)
    }
    timing.FrameEncode = time.Since(frameEncodeStartedAt)

    writeCtx, cancel := context.WithTimeout(ctx, c.writeTimeout)
    defer cancel()

    websocketWriteStartedAt := time.Now()
    if err := c.conn.Write(writeCtx, websocket.MessageBinary, payload); err != nil {
        timing.WebSocketWrite = time.Since(websocketWriteStartedAt)
        return timing, fmt.Errorf("send websocket message: %w", err)
    }
    timing.WebSocketWrite = time.Since(websocketWriteStartedAt)
    c.touchActivity()
    return timing, nil
}
```

Important details to preserve exactly:

- `context.WithTimeout(ctx, c.writeTimeout)` — the new ctx derives from the
  **caller's** ctx, not `context.Background()`. This is the core of the fix.
  `WithTimeout(parent, d)` cancels on whichever fires first: `parent.Done()`
  or the `d` timer.
- `FrameEncode` and `WebSocketWrite` timing must be captured even on the error
  path, matching current `SendWithTiming` behavior (the error-path timing
  assignments exist at `websocket.go:200,205,215`).
- Error wrapping strings (`"build websocket frame"`, `"encode websocket
  frame"`, `"send websocket message"`) stay identical to the current
  `SendWithTiming` strings so downstream callers that match on substrings
  (if any) don't break.
- `net` must be imported in `websocket.go` — it already is, for
  `net.Listen` / `net.ErrClosed`. Confirm no new imports are required. The
  only module-level additions should be the two fields plus the new methods.

### 3. Rewire the public send methods as thin wrappers

- [ ] `Send(msg Message) error` (currently `websocket.go:184-187`) becomes:

  ```go
  func (c *WebSocketConnection) Send(msg Message) error {
      _, err := c.sendContextWithTiming(context.Background(), msg)
      return err
  }
  ```

- [ ] `SendWithTiming(msg Message) (SendTiming, error)` (currently
  `websocket.go:189-221`) becomes:

  ```go
  func (c *WebSocketConnection) SendWithTiming(msg Message) (SendTiming, error) {
      return c.sendContextWithTiming(context.Background(), msg)
  }
  ```

  The previous ~30 lines of body move into `sendContextWithTiming`. Do **not**
  keep the old body around "just in case" — delete it.

### 4. Add the new public `SendContext`

Add a new exported method, placed logically between the wrappers and
`Receive` / `ReceiveContext` so the file reads Send / SendWithTiming /
SendContext / Receive / ReceiveContext:

```go
// SendContext sends a message like Send, but respects the caller's context
// across both the internal write-lock wait and the underlying WebSocket write.
// If ctx is cancelled, SendContext returns promptly with a wrapped ctx.Err
// even if another goroutine currently holds the write lock.
//
// SendContext shares the write lock with Send and SendWithTiming, so mixing
// ctx-aware and non-ctx-aware callers on the same connection is safe —
// non-ctx-aware callers simply cannot abort their own wait.
func (c *WebSocketConnection) SendContext(ctx context.Context, msg Message) error {
    _, err := c.sendContextWithTiming(ctx, msg)
    return err
}
```

Note the godoc explains the interop with existing callers. Do not add any
other exported methods.

### 5. Close must drain waiters

`Close` (currently `websocket.go:250-252`) must close the `closed` channel
exactly once before closing the underlying connection, so any goroutine
parked in the `sendContextWithTiming` select unblocks via the `<-c.closed`
branch and returns a `net.ErrClosed`-wrapped error instead of hanging.

- [ ] Rewrite `Close` as:

  ```go
  func (c *WebSocketConnection) Close() error {
      c.closeOnce.Do(func() { close(c.closed) })
      return c.conn.Close(websocket.StatusNormalClosure, "")
  }
  ```

  Do not change the return semantics or the close status code.

### 6. Confirm the file still compiles and go vet is clean

Run `go build ./...` and `go vet ./...` from the worktree root after each
substantive change. Do not proceed to tests until both are clean.

## Tests

Add the following test functions to `tunnel/tunnel_test.go`. Do **not** modify
any existing test function. Tests are in `package tunnel`, so they have
access to unexported fields (`writeLock`, `closed`) — use that access to
construct deterministic contention scenarios.

### `TestWebSocketConnection_SendContext_HappyPath`

- Use `setupWSPair(t)`.
- Call `client.SendContext(ctx, msg)` with a non-cancelled `context.Background()`-derived ctx and a normal message.
- `srv.Receive()` and verify Type / ID / Body match.
- Assert no error.

This is the "doesn't regress the happy path" check.

### `TestWebSocketConnection_SendContext_CtxCancelledDuringLockWait`

Deterministic lock-contention test:

- `client, _ := setupWSPair(t)` — you only need the client side.
- Manually occupy the write lock from the test goroutine:
  ```go
  client.writeLock <- struct{}{}
  defer func() { <-client.writeLock }()
  ```
  This parks any subsequent `SendContext` call at the lock-acquisition select.
- Create `ctx, cancel := context.WithCancel(context.Background())`.
- In a goroutine, `time.Sleep(20 * time.Millisecond)` then `cancel()`.
- Call `client.SendContext(ctx, Message{Type: MsgPing})`. Assert:
  - Returns a non-nil error.
  - `errors.Is(err, context.Canceled)` is true.
  - Total wall-clock elapsed < 1 second (i.e. the call did NOT wait for
    `writeTimeout`). Use a start timestamp and a generous bound to avoid
    flake.
- Import `errors` in the test file if it is not already imported.

### `TestWebSocketConnection_SendContext_CloseUnblocksWaiters`

Same idea but with `Close` draining the wait instead of ctx cancel:

- `client, _ := setupWSPair(t)`.
- Occupy the write lock manually: `client.writeLock <- struct{}{}`
  (no defer release — `Close` drains via the `closed` channel, not the lock).
- In a goroutine, `time.Sleep(20 * time.Millisecond)` then
  `_ = client.Close()`.
- Call `client.SendContext(context.Background(), Message{Type: MsgPing})`.
  Assert:
  - Returns a non-nil error.
  - `errors.Is(err, net.ErrClosed)` is true.
  - Elapsed < 1 second.
- Import `net` in the test file if it is not already imported (it is).

### `TestWebSocketConnection_SendContext_ConcurrentMixedCallers`

Proves Send/SendContext interop doesn't lose messages or deadlock:

- `client, srv := setupWSPair(t)`.
- Start two goroutines:
  - One calls `client.Send(Message{Type: MsgHTTPRequest, ID: "req-a", Method: "GET", Path: "/a"})` ten times in a loop.
  - The other calls `client.SendContext(ctx, Message{Type: MsgHTTPRequest, ID: "req-b", Method: "GET", Path: "/b"})` ten times in a loop, with a
    non-cancelled `context.Background()`-derived ctx.
- On the `srv` side, call `srv.Receive()` twenty times total, count the
  number of `req-a` vs `req-b` IDs, and assert both equal 10.
- Use a `sync.WaitGroup` to wait for both producers. Use a reasonable ctx
  deadline on the receive loop to avoid hanging the test on regression.
- The test is about "no lost sends, no deadlock under mixed callers"; it
  does NOT need to assert anything about ordering.

### `TestWebSocketConnection_SendContext_CtxCancelDuringWrite` — **best-effort, OK to skip**

Cancelling ctx while `c.conn.Write` is actively blocked on a full kernel
socket buffer is hard to exercise deterministically in a unit test. The
ctx-forwarding correctness is covered by code inspection of
`context.WithTimeout(ctx, c.writeTimeout)` in `sendContextWithTiming` plus
the lock-wait test above. You **do not need** to write this test. If you
find a clean deterministic way to do it inside `tunnel_test.go` without
touching production code, great; otherwise skip it and do not attempt a
flaky version.

## Acceptance criteria

- `tunnel/websocket.go`:
  - `writeMu sync.Mutex` field is gone.
  - `writeLock chan struct{}`, `closed chan struct{}`, `closeOnce sync.Once`
    fields exist and are initialized in `newWebSocketConnection`.
  - `sendContextWithTiming(ctx, msg) (SendTiming, error)` is the single
    workhorse. Ctx is threaded into both the acquisition select and
    `context.WithTimeout(ctx, c.writeTimeout)` for the underlying write.
  - `Send`, `SendWithTiming`, `SendContext` all delegate to
    `sendContextWithTiming`.
  - `Send` / `SendWithTiming` public signatures and behavior are preserved.
  - `Close` closes the `closed` channel exactly once before closing the
    underlying conn.
- `tunnel/tunnel_test.go` has the four new tests above (`CtxCancelDuringWrite`
  may be skipped).
- `git diff` touches only `tunnel/websocket.go` and `tunnel/tunnel_test.go`.
- `go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race ./...`
  are all green.
- No new imports beyond what's already present in the two files (or whatever
  standard-library additions the tests strictly require — `errors` is the
  likely one).

## Verification

From the worktree root:

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

All four must pass. Commit with a descriptive message
(e.g. `fix(proto): add SendContext with ctx-aware write lock`) and stop —
do not push. The lead will review the diff first.
