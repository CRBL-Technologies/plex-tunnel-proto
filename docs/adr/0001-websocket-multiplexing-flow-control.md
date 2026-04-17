# ADR 0001: WebSocket multiplexing flow control

## Status
Accepted

Future changes to these semantics require a new ADR that supersedes or amends
ADR 0001.

## Context
PR #253 on `plex-tunnel-server` moved `/:/websockets/notifications` onto the
singleton control WebSocket and regressed other control traffic through
head-of-line blocking: one busy stream could serialize writes for every other
stream sharing that socket. The tunnel protocol already defines `MsgWSOpen`,
`MsgWSFrame`, and `MsgWSClose` in `tunnel/message.go:12-26`, and the repo
currently advertises `ProtocolVersion = 3` in `tunnel/message.go:38-40`.
Stream identity already uses the existing string `Message.ID` field in
`tunnel/message.go:44-70`, with validation for WebSocket open, frame, and close
messages in `tunnel/message.go:127-140`. What remains undefined is how
multiple WebSocket streams share one control socket, how per-stream
backpressure is applied, and what a receiver does when a sender outruns the
credit it has been granted.

## Decision
- Use per-stream credit-based flow control on the existing control WebSocket,
  inspired by HTTP/2 `WINDOW_UPDATE`, and do not add a separate connection-level
  window. Tunnel-level backpressure is already provided by the underlying
  WebSocket TCP connection, so a second connection-wide credit layer would be
  redundant with little benefit.
- Reserve `MsgWSWindowUpdate` as message type `14`, the next unused value after
  `MsgCancel` in `tunnel/message.go:12-26`; the valid-range comment in
  `tunnel/frame.go:26-34` must be updated in the follow-up implementation.
- Add `WindowIncrement uint32` with JSON field `window_increment` to both
  `Message` and `frameMetadata` in a follow-up code change at
  `tunnel/message.go:44-70` and `tunnel/frame.go:41-58`. `WindowIncrement`
  represents raw bytes of additional credit, matching `MsgWSFrame` body-length
  semantics rather than RFC 6455 fragment tokens.
- Require `MsgWSWindowUpdate` to carry a stream `ID`, a zero-length body, and a
  positive `WindowIncrement`. A non-zero body is a protocol error. A
  `WindowIncrement` of zero is a stream-scoped protocol error: the receiver
  sends `MsgError`, closes that stream with `MsgWSClose` status `1008`, and
  keeps the rest of the session alive.
- Cap cumulative per-stream credit so the granted-but-unconsumed window never
  exceeds `2^31 - 1`. If the sum of received `WindowIncrement` values minus
  bytes consumed would exceed that bound, the receiver treats it as a
  `FLOW_CONTROL_ERROR`, sends `MsgError`, closes only that stream with status
  `1008`, and keeps the session intact.
- Set the initial per-stream send window to `64 KiB` (`65536` bytes). This ADR
  does not introduce a SETTINGS frame, so the initial window is fixed until a
  later ADR revisits tuning or negotiation.
- Require senders to decrement local credit by `len(Body)` before each
  `MsgWSFrame`, and to block instead of dropping data if the next frame would
  exceed available credit. `MsgWSFrame` body length MUST be less than or equal
  to the initial window size (`65536` bytes); a larger `MsgWSFrame` is a
  connection-level protocol error.
- If a logical WebSocket payload exceeds `65536` bytes, the sender MUST split it
  across multiple `MsgWSFrame` messages while preserving logical ordering via
  the stream `ID`. The mechanism for preserving WebSocket frame or message
  boundaries across those splits is explicitly out of scope for this ADR; the
  implementing server and client PRs will define it, or a follow-up ADR will.
- Require receivers to replenish credit only after bytes are consumed by the
  local WebSocket peer. Implementations should emit `MsgWSWindowUpdate` once at
  least half the initial window (`32768` bytes) has been consumed, which limits
  chatter while restoring throughput promptly.
- Keep stream IDs as the existing UUID-style `Message.ID` strings; no numeric
  even/odd allocation is introduced because UUIDs already provide
  zero-coordination stream creation for both HTTP and WebSocket streams.
- Negotiate flow-control support through Register/RegisterAck capability bits
  rather than a protocol-version bump. `ProtocolVersion` is already `3`, and
  `Capabilities uint32` already exists on `Message` and `frameMetadata` in
  `tunnel/message.go:55-57` and `tunnel/frame.go:48-49`, so the follow-up code
  should add `CapWSFlowControl = 1 << 1`. Flow control is enabled only if both
  peers advertise `CapWSFlowControl`; otherwise both sides fall back to today's
  no-credit behavior and must not emit `MsgWSWindowUpdate`.
- Treat send-credit overruns as stream-scoped protocol violations: the receiver
  sends `MsgError` with an explanatory reason, then closes only the offending
  stream with `MsgWSClose` status `1008`, leaving the rest of the tunnel alive.
- On `MsgWSClose`, both sides immediately drop stream-level credit state for
  that `ID`. Any in-flight `MsgWSFrame` that arrives after `MsgWSClose` for the
  same stream ID is silently discarded.
- `MsgWSWindowUpdate` shares the same control-WebSocket write path as
  `MsgWSFrame`, so a window update for stream A can sit behind a frame for
  stream B. This ADR accepts that residual head-of-line effect because window
  updates are tiny fixed-size control messages; no priority mechanism is added
  here.

## Alternatives considered

### Separate WS-mux channel class
Adding a third tunnel class between the singleton control socket and the leased
data pool would increase routing and topology complexity without fixing the
actual failure mode. The regression was caused by one stream starving its
peers, not by traffic using the wrong physical tunnel identity. Flow control on
the existing control socket addresses the problem directly with less machinery.

### Piggyback credits on `MsgWSFrame`
Attaching credit acknowledgements to `MsgWSFrame` would couple replenishment to
reverse-direction data flow. A receiver that has consumed bytes but has no
payload to send back would have no clean way to restore the sender's credit.
A dedicated `MsgWSWindowUpdate` frame keeps credit signalling explicit and
independent of whether application data happens to be flowing the other way.

### Numeric even/odd stream IDs
HTTP/2-style even/odd stream numbering is unnecessary here because the
protocol already uses the string `ID` field for request and WebSocket stream
identity. Switching to numeric allocation would churn existing plumbing in
`tunnel/message.go` and related callers without solving a real coordination
problem that UUIDs have already eliminated.

### Fair-share scheduler at the tunnel layer
A fair-share writer can decide which stream gets the next send slot, but it
does not create receiver-side backpressure. A slow or stalled local consumer
still pins buffered data and eventually blocks peers on the shared socket.
Credits solve both fairness and bounded buffering in a way a scheduler alone
does not.

### Rely on TCP flow control only
TCP flow control operates at the shared connection level, not at the level of
individual multiplexed streams. One stalled stream can therefore consume the
available connection window and starve unrelated streams on the same control
WebSocket. Per-stream credits are required to prevent that cross-stream
interference.

## Consequences
- `/:/websockets/notifications` and other control-class WebSocket streams can
  coexist on the control socket without one stream monopolizing all forward
  progress.
- The change is additive at the frame layer because `MsgWSWindowUpdate` is a
  new type and flow control is gated by an explicit capability bit.
- Old peers remain interoperable with current behavior, but they do not gain
  per-stream backpressure until both sides advertise `CapWSFlowControl`.
- Receiver buffering is not negligible: before the local consumer drains, a
  receiver may need to buffer up to the sum of `window_size` across active
  streams, and that memory stays pinned if local Plex stalls. At `64 KiB` per
  stream, `50` concurrent WebSocket streams imply a worst-case receiver buffer
  of about `3.2 MiB` per tunnel.
- Implementations must add chunking for logical payloads larger than `64 KiB`,
  and residual head-of-line delay for tiny `MsgWSWindowUpdate` messages remains
  possible because updates share the control-WebSocket write path with data.

## Follow-up work
- `plex-tunnel-proto`: add `MsgWSWindowUpdate`, `WindowIncrement`,
  `CapWSFlowControl = 1 << 1`, validation rules, and tests; update
  `tunnel/frame.go` metadata encoding and the valid-range comment.
- `plex-tunnel-server`: implement control-WebSocket multiplex routing with
  per-stream send and receive credit accounting, chunking for oversized logical
  payloads, and stream-scoped violation handling.
- `plex-tunnel` issue `#92`: implement client-side WebSocket demux, bridge
  consumed-byte accounting to the local Plex socket, and emit window updates.
- If split-frame boundary preservation proves ambiguous during implementation,
  write a follow-up ADR that formalizes those semantics without changing the
  flow-control rules accepted here.
