# Wire Protocol v4

## Summary

Protocol v4 adds unified-pool per-frame placement support through `Seq`, `MsgFrameDelivered`, and `CapUnifiedPool`, makes cancel signaling symmetric with bidirectional `MsgCancel` plus `MsgCancelAck`, introduces HTTP flow-control credit with `MsgHTTPWindowUpdate`, and adds an observability-only `TunnelUID` on register acknowledgements. `ProtocolVersion=4` requires an exact handshake match with no fallback. Full rationale lives in [issue #298](https://github.com/CRBL-Technologies/plex-tunnel-server/issues/298).

## New message types

| Type | Value | Direction | Purpose |
| --- | ---: | --- | --- |
| `MsgHTTPWindowUpdate` | 15 | peer -> peer | Grants additional HTTP stream flow-control credit using `ID` and `WindowIncrement`. |
| `MsgCancelAck` | 16 | peer -> peer | Acknowledges a prior `MsgCancel` for the same stream `ID`. |
| `MsgFrameDelivered` | 17 | peer -> peer | Confirms downstream delivery through `AckedSeq` and optionally marks terminal delivery with `StreamTerminal=true`. |

## Changed message semantics

`MsgCancel` is bidirectional in v4; v3 treated it as server -> client only. All other v3 message semantics are preserved.

## New `frameMetadata` fields

| Field | JSON key | Populated on | Purpose |
| --- | --- | --- | --- |
| `Seq` | `seq` | Seq-tracked stream frames | Carries the per-stream monotonic sequence number used for unified-pool reassembly and delivery tracking. |
| `TunnelUID` | `tunnel_uid` | `MsgRegisterAck` | Server-assigned observability identifier for a tunnel. It is never used for routing decisions. |
| `AckedSeq` | `acked_seq` | `MsgFrameDelivered` | Reports the highest per-stream sequence number delivered downstream. |
| `StreamTerminal` | `stream_terminal` | `MsgFrameDelivered` | Marks that the terminal frame has been delivered and no further delivery acknowledgements will follow for the stream. |

## Capability negotiation

`CapUnifiedPool = 1 << 2`. Both peers MUST advertise this capability during register/register-ack for unified-pool semantics to be enabled. A capability mismatch is rejected by the server with `MsgError{Error: "v4_capability_required"}`. There is no silent fallback.

## What is NOT in v4

- Tunnel index
- `RouteClass`
- `pendingStreamIDs` set
- `recentEgressStreamIDs` set
- Placement cost function

These are implementation concerns, not wire-level protocol fields.
