# plex-tunnel-proto v2.0.0 — unified-pool wire additions (#298 Phase A)

Full spec: https://github.com/CRBL-Technologies/plex-tunnel-server/issues/298 (round-4-final body).

Base: `origin/main` (proto uses `main` as release target per existing gitflow; see how `fix/297-validator-relax` was merged).
Target PR: `feat/298-unified-pool-proto-v4` → `main`.
Tag: `v2.0.0` to be cut by CEO after merge — **do NOT tag yourself**.

## Context

Issue #298 retires the control-vs-data tunnel classification and introduces per-frame placement across a unified tunnel pool. Receivers must reassemble by Seq, both peers must emit delivery acks so senders can safely scrub per-tunnel "recent egress" sets on tunnel death, HTTP streams gain a credit signal parallel to `MsgWSWindowUpdate`, and `MsgCancel` becomes bidirectional with an ack message.

This is Phase A: **proto wire additions only**. No server or client code changes. The bump is a major version (`v2.0.0`) because `ProtocolVersion` moves from 3 → 4 and peers enforce exact match at register — any v3 peer hitting a v4 peer fails the handshake by design.

## Goal

Ship `plex-tunnel-proto` v4 wire definitions that are JSON-round-trip safe, covered by Validate() checks, and documented in `docs/wire-v4.md`. Green tests. Nothing else.

## Constraints / guardrails

- **DO NOT delete or modify code not mentioned in this task.** In particular: do not change any existing message validation logic, frame marshal/unmarshal body, or WebSocket transport code.
- Do NOT alter `CapLeasedPool` or `CapWSFlowControl` values or meanings (cap bits are ordered; reserve `1 << 2` for the new cap).
- Do NOT remove any v3 message types, fields, or capabilities.
- Do NOT tag `v2.0.0`.
- Do NOT bump the module path's major version via `/v2` import rewrite — proto is not semantically-versioned-import-path-gated; the git tag is the release marker.

## Tasks

### 1. `tunnel/message.go`

- [ ] **Bump `ProtocolVersion`**: `const ProtocolVersion uint16 = 4` (was 3). Keep the comment accurate.
- [ ] **Add capability bit**: below `CapWSFlowControl uint32 = 1 << 1`, add `CapUnifiedPool uint32 = 1 << 2` with a comment:
      `// CapUnifiedPool indicates the peer supports the unified-pool data plane (per-frame placement across untyped tunnels, reassembly by Seq, delivery acks via MsgFrameDelivered). Both peers MUST advertise for the session to use unified-pool semantics.`
- [ ] **Add three new `MessageType` constants** at the end of the existing `const ( MsgRegister MessageType = iota + 1 … MsgWSWindowUpdate )` block:
  - `MsgHTTPWindowUpdate  // HTTP flow-control credit grant (parallel to MsgWSWindowUpdate)` — value 15.
  - `MsgCancelAck         // acknowledges a prior MsgCancel (either direction)` — value 16.
  - `MsgFrameDelivered    // receiver-emitted delivery confirmation; carries AckedSeq + optional StreamTerminal` — value 17.
- [ ] **Add four new fields to `Message` struct** (place after `WindowIncrement` and before `Encrypted`; maintain alphabetical-like grouping where reasonable):
  - `Seq uint64             \`json:"seq,omitempty"\`` — per-stream monotonic sequence number on Seq-tracked frames (MsgHTTPRequest/Response continuations, MsgWSFrame, MsgWSClose, stream-open frames with Seq=0). Absent on stream-control and session-primary messages.
  - `TunnelUID string       \`json:"tunnel_uid,omitempty"\`` — server-assigned opaque observability identifier for a tunnel. Populated only on MsgRegisterAck (server→client). NEVER consulted for routing. Logs, /debug/tunnels, trace events only.
  - `AckedSeq uint64        \`json:"acked_seq,omitempty"\`` — highest per-stream Seq the receiver has delivered downstream. Populated only on MsgFrameDelivered.
  - `StreamTerminal bool    \`json:"stream_terminal,omitempty"\`` — MsgFrameDelivered sets `true` when the terminal frame (EndStream=true / MsgWSClose) has been delivered downstream and no further delivery acks will be emitted for this stream.
- [ ] **Add Validate() cases** inside `func (m Message) Validate() error`:
  - `case MsgHTTPWindowUpdate:` — require `m.ID != ""`; require `m.WindowIncrement > 0`. Mirror the shape of the existing `MsgWSWindowUpdate` case.
  - `case MsgCancelAck:` — require `m.ID != ""`. Mirror `MsgCancel`.
  - `case MsgFrameDelivered:` — require `m.ID != ""`. `AckedSeq` may be 0 (valid for a stream whose first Seq=0 frame was just delivered). `StreamTerminal` is a pure bool; no validation.

### 2. `tunnel/frame.go`

- [ ] **Add the four new fields to `frameMetadata` struct**: mirror the `Message` struct additions. Same JSON tags.
- [ ] **Update wire-format comment block at lines ~25-34**: change `"valid types are in [1, 14] for protocol v3 with CapWSFlowControl"` to `"valid types are in [1, 17] for protocol v4 with CapUnifiedPool"`. Preserve the rest of the comment verbatim.
- [ ] **Update `encodeFrameMetadata`** to populate `Seq`, `TunnelUID`, `AckedSeq`, `StreamTerminal` from the `Message`.
- [ ] **Update `decodeFrameMetadata`** to populate the `Message` return value with the four fields from `meta`.

### 3. `docs/wire-v4.md` — new file

- [ ] Create `docs/wire-v4.md`. Required sections:
  - **Summary** — one paragraph: v4 adds per-frame placement support (Seq + MsgFrameDelivered + CapUnifiedPool), symmetric cancel (bidirectional MsgCancel + MsgCancelAck), HTTP flow control (MsgHTTPWindowUpdate), and an observability-only TunnelUID. ProtocolVersion=4 exact match required; no fallback.
  - **New message types** — table: type name, numeric value, direction (sender → receiver), purpose. Include MsgHTTPWindowUpdate, MsgCancelAck, MsgFrameDelivered.
  - **Changed message semantics** — `MsgCancel` is now bidirectional (v3 was server→client only); all other v3 message semantics are preserved.
  - **New `frameMetadata` fields** — table: field name, JSON key, populated on, purpose.
  - **Capability negotiation** — `CapUnifiedPool = 1 << 2`. Both peers MUST advertise at register; mismatch → server rejects with `MsgError{Error: "v4_capability_required"}`. No silent fallback.
  - **What is NOT in v4** — explicit list of things that are server/client-internal and not on the wire: tunnel index, RouteClass, pendingStreamIDs set, recentEgressStreamIDs set, placement cost function. These are implementation concerns.
  - Keep the doc under 300 lines. Link to issue #298 for full rationale.

### 4. Tests — `tunnel/tunnel_test.go`

- [ ] **Extend `TestMessageValidate`** table (currently at line 17) with entries for the three new message types:
  - `"http window update ok"` — `{Type: MsgHTTPWindowUpdate, ID: "stream-1", WindowIncrement: 65536}`, expect success.
  - `"http window update missing id"` — `{Type: MsgHTTPWindowUpdate, WindowIncrement: 65536}`, `wantErr: true`.
  - `"http window update zero increment"` — `{Type: MsgHTTPWindowUpdate, ID: "stream-1"}`, `wantErr: true`.
  - `"cancel ack ok"` — `{Type: MsgCancelAck, ID: "stream-1"}`, success.
  - `"cancel ack missing id"` — `{Type: MsgCancelAck}`, `wantErr: true`.
  - `"frame delivered ok"` — `{Type: MsgFrameDelivered, ID: "stream-1", AckedSeq: 5}`, success.
  - `"frame delivered terminal ok"` — `{Type: MsgFrameDelivered, ID: "stream-1", AckedSeq: 42, StreamTerminal: true}`, success.
  - `"frame delivered zero seq ok"` — `{Type: MsgFrameDelivered, ID: "stream-1"}`, success (AckedSeq=0 is legitimate for first-frame ack).
  - `"frame delivered missing id"` — `{Type: MsgFrameDelivered, AckedSeq: 5}`, `wantErr: true`.
- [ ] Ensure existing register / ack / other test cases still pass with `ProtocolVersion: 4`. If any test case pins `ProtocolVersion: 3` as valid, update to 4.

### 5. Tests — `tunnel/frame_test.go`

- [ ] **Add `TestFrameV4FieldsRoundTrip`**: construct a Message with non-zero Seq, TunnelUID, AckedSeq, StreamTerminal; NewFrame → MarshalBinary → UnmarshalFrame → Message; assert all four fields preserved.
- [ ] **Add `TestFrameMsgFrameDeliveredRoundTrip`**: full MsgFrameDelivered with `ID, AckedSeq, StreamTerminal=true` survives round-trip.
- [ ] **Add `TestFrameMsgHTTPWindowUpdateRoundTrip`**: `MsgHTTPWindowUpdate{ID, WindowIncrement=1<<20}` survives round-trip.
- [ ] **Add `TestFrameMsgCancelAckRoundTrip`**: `MsgCancelAck{ID}` survives round-trip.
- [ ] **Add `TestFrameOmitemptyV4Fields`**: Message without Seq/TunnelUID/AckedSeq/StreamTerminal (zero values) produces metadata JSON that does NOT contain the `seq`, `tunnel_uid`, `acked_seq`, or `stream_terminal` keys (verify by unmarshalling header bytes into a `map[string]any`).

### 6. Test — `tunnel/message_test.go`

- [ ] If a test pins `ProtocolVersion == 3`, update to 4. Otherwise no change.

## Tests summary

All tests added per above.  Full test targets: `TestMessageValidate/*`, `TestFrameV4FieldsRoundTrip`, `TestFrameMsgFrameDeliveredRoundTrip`, `TestFrameMsgHTTPWindowUpdateRoundTrip`, `TestFrameMsgCancelAckRoundTrip`, `TestFrameOmitemptyV4Fields`.

## Acceptance criteria

- `ProtocolVersion == 4`. `CapUnifiedPool == 1 << 2`. `MsgHTTPWindowUpdate == 15`, `MsgCancelAck == 16`, `MsgFrameDelivered == 17`.
- `Message` and `frameMetadata` each carry `Seq`, `TunnelUID`, `AckedSeq`, `StreamTerminal` with JSON tags `seq,omitempty` / `tunnel_uid,omitempty` / `acked_seq,omitempty` / `stream_terminal,omitempty`.
- `Validate()` accepts valid / rejects invalid instances of the three new message types per the rules above.
- JSON round-trip via `NewFrame` → `MarshalBinary` → `UnmarshalFrame` → `Message` preserves all v4 field values.
- Zero-value v4 fields are omitted from the metadata JSON (backward-JSON-compatible with v3 peers that decode a v4 frame, though v3 peers are rejected at register).
- `docs/wire-v4.md` created with sections as listed.
- No v3 message types / fields / caps removed.

## Verification

```
cd /home/dev/worktrees/noah/plex-tunnel-proto
go build ./...
go test ./...
go vet ./...
```

All three must pass. No new golangci-lint warnings (run `golangci-lint run ./...` if the tool is installed locally; CI will run it anyway).

## Out of scope (Phase B / Phase C — later PRs)

- Server-side unified pool, per-frame placement, reassembly, tombstones, primary failover.
- Client-side classifier removal, `wsStream` refactor, `pendingStreamIDs` / `recentEgressStreamIDs` sets.
- `/debug/tunnels` endpoints.
- Any behavior change in existing `plex-tunnel-server` or `plex-tunnel` code.

These land in later PRs per the round-4-final body's phased rollout.
