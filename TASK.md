# TASK: long-lived tunnel WebSocket read timeout (P0)

## Context

Staging download failures (2026-04-09) traced to a **60-second pool recycle pattern**: every ~60s the full data-tunnel pool tears down and rebuilds. Root cause is the **30-second per-`Receive()` read timeout** in this package:

- `tunnel/websocket.go:22` → `defaultReadTimeout = 30 * time.Second`
- `tunnel/websocket.go:272-273` wraps every `Receive()` call in `context.WithTimeout(parent, readTimeout)`

Under the new leased-tunnel-pool architecture (merged yesterday), a data tunnel carrying a long download has `streams > 0` for the whole transfer but **no inbound messages** flow to its read loop (request frame already consumed; only outbound `HTTPResponse` chunks flow back). After 30s of inbound silence the deadline fires and downstream consumers (server `pkg/server/server.go:887`, client `pkg/client/client.go:274`) close the tunnel mid-stream. The full pattern is 30s-of-life + 30s-reconnect-backoff = the observed 60s cadence. Sam's log analysis confirms this: tunnel 97 in his report died at 56s with `"failed to read frame payload: unexpected EOF"` — exactly what a mid-stream client-side close produces on the server's reader.

The 30s per-read deadline is correct for short-lived WebSocket uses (the WS-proxy path that opens a tunnel per downstream WebSocket request), but **wrong for the long-lived control and data tunnels** which rely on application-layer ping/pong for liveness.

CEO is actively testing on staging. This proto fix is the gate — server and client P0 fixes both depend on the new entry point this task exposes.

## Goal

Expose long-lived-tunnel dial/accept entry points that disable the per-`Receive()` read deadline. Keep the existing `DialWebSocket` / `AcceptWebSocket` behavior unchanged for generic callers. Server and client will migrate their tunnel-open call sites to the new entry points in separate PRs.

## Constraints / guardrails

- **DO NOT delete or modify code not mentioned in this task.**
- **DO NOT change the default read timeout for existing `DialWebSocket` / `AcceptWebSocket` callers.** Short-lived WS-proxy users must keep their 30s bound.
- **DO NOT change the ping/pong protocol or any message types.** This is a transport-layer fix only.
- **DO NOT touch** `tunnel/message.go`, `tunnel/striping.go`, or any protocol-level file — stay in `tunnel/websocket.go` and `tunnel/tunnel_test.go`.
- Semver: this is additive (new exported functions). Must not break any existing consumer of `DialWebSocket` or `AcceptWebSocket`.
- The new read-timeout behavior must be **zero = no per-read deadline**, not a large-but-finite duration. Finite-but-large would still produce spurious deadlines under pathological network stalls.

## Tasks

### 1. Add `TunnelReadTimeout` constant

File: `tunnel/websocket.go`, near the existing `defaultReadTimeout` / `defaultWriteTimeout` constants (~line 18-23).

Add:

```go
// TunnelReadTimeout is the per-Receive read deadline for long-lived tunnel
// WebSocket connections. It is zero — no deadline is applied to individual
// reads — because tunnel liveness is managed by application-layer ping/pong
// (see plex-tunnel-server and plex-tunnel client keepalive loops). Short-lived
// callers (e.g. the WS-proxy pass-through path) should continue to use
// defaultReadTimeout via DialWebSocket / AcceptWebSocket.
TunnelReadTimeout = 0 * time.Second
```

### 2. Teach `ReceiveContext` to treat `readTimeout <= 0` as "no per-read deadline"

File: `tunnel/websocket.go`, in `ReceiveContext` (~line 269-290).

Current code:

```go
func (c *WebSocketConnection) ReceiveContext(parent context.Context) (Message, error) {
    ctx := parent
    var cancel context.CancelFunc
    if c.readTimeout > 0 {
        ctx, cancel = context.WithTimeout(parent, c.readTimeout)
        defer cancel()
    }
    ...
}
```

Audit this path — the `c.readTimeout > 0` guard already exists, so passing `0` through the constructor should already disable the deadline. **Verify** that when `newWebSocketConnection` is called with `readTimeout = 0`, the resulting connection's `Receive()` calls block indefinitely (until the parent context or the underlying conn closes).

If the guard is already correct, no code change is needed here — just a unit test (see task 5).

### 3. Add `DialTunnelWebSocket` exported function

File: `tunnel/websocket.go`, adjacent to the existing `DialWebSocket` (~line 88-95).

```go
// DialTunnelWebSocket is like DialWebSocket but configures the connection for
// long-lived tunnel use: no per-Receive read deadline. Liveness detection is
// the caller's responsibility via application-layer ping/pong. Used by the
// plex-tunnel client to open control and data tunnels.
func DialTunnelWebSocket(ctx context.Context, rawURL string, headers http.Header) (*WebSocketConnection, error) {
    return dialWebSocket(ctx, rawURL, headers, TunnelReadTimeout, defaultWriteTimeout)
}
```

Keep the existing `DialWebSocket` signature and behavior unchanged — it must continue to use `defaultReadTimeout` (30s).

### 4. Add `AcceptTunnelWebSocket` exported function

File: `tunnel/websocket.go`, adjacent to the existing `AcceptWebSocket` (~line 97-102).

```go
// AcceptTunnelWebSocket is like AcceptWebSocket but configures the connection
// for long-lived tunnel use: no per-Receive read deadline. Liveness detection
// is the caller's responsibility via application-layer ping/pong. Used by the
// plex-tunnel-server tunnel-mux handler to accept client tunnel connections.
func AcceptTunnelWebSocket(w http.ResponseWriter, r *http.Request) (*WebSocketConnection, error) {
    return acceptWebSocket(w, r, TunnelReadTimeout, defaultWriteTimeout)
}
```

Keep the existing `AcceptWebSocket` signature and behavior unchanged.

### 5. Unit tests (add to `tunnel/tunnel_test.go`)

All in the same file. Do not create new test files.

- [ ] **`TestDialTunnelWebSocket_NoReadDeadline`** — open a tunnel pair via `AcceptTunnelWebSocket` / `DialTunnelWebSocket`. On the receiving side, call `Receive()` in a goroutine. Sleep 1.5 × `defaultReadTimeout` (= 45 seconds) without sending any message. **Assert the Receive() has not returned.** Then send a message from the other side and assert it is delivered successfully. Use `t.Skip` if short-mode (`testing.Short()`) because of the long sleep.
- [ ] **`TestDialWebSocket_StillHasReadDeadline`** — regression test: open a *regular* (non-tunnel) pair via `AcceptWebSocket` / `DialWebSocket`. Call `Receive()` in a goroutine. Assert it returns `context.DeadlineExceeded` within ~35 seconds (30s default + small fudge). Again `t.Skip` on `testing.Short()`. This proves we did not accidentally break the short-lived path.
- [ ] **`TestTunnelReadTimeout_Constant`** — trivial assertion that `TunnelReadTimeout == 0`. Guards against accidental future edits.

### 6. Verify `tunnel_test.go` existing tests still pass

Run `go test ./tunnel/... -count=1`. Existing tests must continue to pass, including:
- `TestWebSocketConnection_*` family
- Tests at `tunnel_test.go:618-622` which assert `transport.ReadTimeout == defaultReadTimeout`
- Tests at `tunnel_test.go:632-646` which exercise `transport.readTimeout()` with various configurations

None of these should need modification — the existing defaults are untouched.

## Acceptance criteria

- `tunnel.TunnelReadTimeout` exported as `0 * time.Second` with a comment explaining why.
- `tunnel.DialTunnelWebSocket(ctx, url, headers)` exists and returns a `*WebSocketConnection` configured with `TunnelReadTimeout`.
- `tunnel.AcceptTunnelWebSocket(w, r)` exists and returns a `*WebSocketConnection` configured with `TunnelReadTimeout`.
- `tunnel.DialWebSocket` and `tunnel.AcceptWebSocket` are **unchanged** — same signatures, same behavior, same 30s `defaultReadTimeout`.
- New unit tests pass and demonstrate both the "no deadline on tunnel" behavior and the "still has deadline on regular" behavior.
- All existing `tunnel/...` tests pass with `-race`.
- `go vet ./...` clean.

## Verification

```bash
cd /home/dev/worktrees/paul/plex-tunnel-proto
go vet ./...
go test -race ./tunnel/... -count=1
```

Both must be clean. If the long-sleep tests are painful in normal runs, gate them on `!testing.Short()` — but they MUST run in CI.

## Notes for reviewer

This is the gating proto change. After this merges to `dev`:
- `plex-tunnel-server` migrates its tunnel-mux `AcceptWebSocket` call to `AcceptTunnelWebSocket` + drops the "streams > 0 → disconnect" branch in its recv loop.
- `plex-tunnel` (client) migrates its `DialWebSocket` calls for control + data tunnels to `DialTunnelWebSocket` + drops the equivalent branch in its readLoop.

Server and client PRs are P0 siblings of this one and will reference this module version.
