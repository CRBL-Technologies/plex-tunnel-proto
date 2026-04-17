# ADR 0001: WebSocket multiplexing flow control

## Status
Proposed

## Context
PR #253 on `plex-tunnel-server` routed `/:/websockets/notifications` onto the
singleton control WebSocket and regressed other control traffic through
head-of-line blocking: one busy WebSocket stream could serialize writes for
every other multiplexed stream on that socket. The tunnel protocol already
defines `MsgWSOpen`, `MsgWSFrame`, and `MsgWSClose` in
`tunnel/message.go:12-26`, and the repo currently advertises
`ProtocolVersion = 3` in `tunnel/message.go:38-40`. Stream identity already
uses the existing string `Message.ID` field in `tunnel/message.go:44-70`, with
validation for WebSocket open/frame/close messages in
`tunnel/message.go:127-140`. What remains undefined is how multiple
WebSocket streams share one control socket, how backpressure is applied per
stream, and what happens when a sender outruns the receiver.

## Decision
- Use per-stream credit-based flow control on the existing control WebSocket,
  inspired by HTTP/2 `WINDOW_UPDATE`, rather than adding a new channel class.
- Reserve `MsgWSWindowUpdate` as message type `14`, the next unused value after
  `MsgCancel` in `tunnel/message.go:12-26`; `tunnel/frame.go:26-34` should be
  updated in the follow-up implementation to reflect the expanded valid range.
- Add `WindowIncrement uint32` with JSON field `window_increment` to both
  `Message` and `frameMetadata` in a follow-up code change at
  `tunnel/message.go:44-70` and `tunnel/frame.go:41-58`; zero is invalid and
  treated as a protocol error, while negative values are excluded by type.
- Set the initial per-stream send window to `64 KiB` (`65536` bytes); this ADR
  does not introduce a SETTINGS frame, so the initial window stays fixed until
  a later ADR revisits negotiation.
- Require senders to decrement local credit by `len(Body)` before each
  `MsgWSFrame`; if the next frame would exceed available credit, the sender
  blocks instead of dropping data.
- Require receivers to replenish credit only after bytes are consumed by the
  local WebSocket peer; implementations should emit `MsgWSWindowUpdate` after
  at least half the initial window (`32 KiB`) has been consumed to limit
  chatter while still restoring throughput promptly.
- Keep stream IDs as the existing UUID-style `Message.ID` strings; no numeric
  even/odd allocation is introduced because UUIDs already support
  zero-coordination stream creation for both HTTP and WebSocket streams.
- Keep multiplexing on the control WebSocket and negotiate support through
  Register/RegisterAck capability bits rather than a protocol-version bump:
  `ProtocolVersion` is already `3`, so follow-up code should add a dedicated
  capability such as `CapWSFlowControl`; peers that do not negotiate that
  capability must keep today's no-credit behavior and must not emit
  `MsgWSWindowUpdate`.
- Treat send-credit overruns as stream-scoped protocol violations: the receiver
  should send `MsgError` with an explanatory reason, then close only the
  offending stream with `MsgWSClose` status `1008`, leaving the rest of the
  tunnel session intact.

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

## Consequences
- `/:/websockets/notifications` and other control-class WebSocket streams can
  coexist on the control socket without one stream monopolizing writes.
- The change is additive at the frame layer because `MsgWSWindowUpdate` is a
  new type and flow-control support is gated by capability negotiation.
- Old peers remain interoperable with current behavior, but they do not gain
  per-stream backpressure until both sides advertise the new capability.
- Implementations must maintain small per-stream credit counters and consumed
  byte counters; the state cost is negligible, but the accounting logic is new.
- The fixed `64 KiB` window is a reasonable starting point, not a final tuning
  guarantee; later measurements may justify a negotiated setting.

## Follow-up work
- `plex-tunnel-proto`: add `MsgWSWindowUpdate`, `WindowIncrement`, validation,
  capability negotiation support, and tests; update `tunnel/frame.go` comments
  and metadata encoding/decoding to match the new frame.
- `plex-tunnel-server`: implement control-WebSocket multiplex routing with
  per-stream send/receive credit accounting and `MsgError` plus `MsgWSClose`
  handling for credit violations.
- `plex-tunnel` issue `#92`: implement client-side WebSocket demux, bridge
  consumed-byte accounting to the local Plex socket, and emit window updates.
- Document the new capability bit in Register/RegisterAck behavior so mixed
  peers have an explicit fallback path and never send `MsgWSWindowUpdate`
  without negotiated support.
