# Bandwidth Investigation — Binary Protocol v1

## Status

Logging instrumentation in progress. This document tracks findings and open questions.

---

## Context

Slow bandwidth reported after deploying the binary protocol (v1) to production for the first time. Both the server (PR #31) and client (PR #1) were merged to their respective `main` branches before the issue was noticed.

This is distinct from the proto module migration (PR #32 server / PR #2 client), which is **not yet on either `main` branch** at the time of this investigation.

---

## What changed: old protocol vs binary protocol v1

### Old protocol (JSON text frames)

- `Body []byte` was tagged `json:"body,omitempty"` — included in the JSON payload as a base64 string
- Each 64 KB video chunk encoded to ~85 KB of base64 + JSON wrapping
- Sent as `websocket.MessageText`

### Binary protocol v1 (current)

- `Body []byte` is tagged `json:"-"` — excluded from JSON
- Wire format: `[1 byte type][4 bytes metadata len][4 bytes body len][JSON metadata][raw binary body]`
- Each 64 KB video chunk encodes to 64 KB + ~50 byte overhead (9-byte fixed header + JSON metadata with just type/ID for continuation chunks)
- Sent as `websocket.MessageBinary`

**On wire volume alone, the binary protocol sends less data per chunk.** A bandwidth regression from this alone would be surprising.

---

## Segment map

For a typical Plex video stream, data flows through four segments:

```
[Plex] --(1)--> [tunnel client] --(2)--> [tunnel server] --(3)--> [browser]
```

1. **Plex → client**: local HTTP, should be fast
2. **Client → server**: WebSocket tunnel, the suspected bottleneck
3. **Server → browser**: server-side HTTP flush loop

The user observed "slow bandwidth from client to server", pointing at segment 2.

---

## Architecture constraints relevant to bandwidth

### Single shared WebSocket connection

Each tunnel session uses **one WebSocket connection** between client and server. All concurrent request/response streams share this connection.

- **On the client side**: `writeMu` in `WebSocketConnection.Send` serializes all writes. While one goroutine is sending a 64 KB video chunk, all other response streams are blocked.
- **On the server side**: the `handleTunnel` read loop calls `deliverResponse`, which does a blocking channel send (`ch <- msg`). The pending channel has a buffer of 32 messages (~2 MB). If the downstream HTTP writer is slow, the channel fills and `deliverResponse` blocks, which stalls the **entire** read loop — no other responses can be delivered while one stream is blocked.

These constraints existed in the old JSON protocol too. However, the binary protocol now sends data more quickly (less serialization overhead), which could amplify contention that was previously masked by slower encoding.

### Response chunk size

The client reads Plex responses in `ResponseChunkSize` chunks (default 64 KB) and sends each as a separate tunnel message. Smaller chunks mean more mutex acquisitions and more JSON encoding per byte.

---

## Hypotheses ranked by likelihood

| # | Hypothesis | How to confirm |
|---|---|---|
| 1 | **`writeMu` head-of-line blocking**: concurrent streams contend on one write lock; one slow stream stalls others | Log time spent waiting for `writeMu` in `Send`; compare across concurrent requests |
| 2 | **Server-side `pending` channel back-pressure**: slow downstream browser blocks the tunnel read loop | Log time between `deliverResponse` call and channel send completing |
| 3 | **Chunk size too small for the new protocol**: 64 KB is fine for JSON but too many round-trips for binary | Test with `PLEXTUNNEL_RESPONSE_CHUNK_SIZE=262144` or `524288` |
| 4 | **Unrelated regression**: something else changed in the same deployment window (Caddy, Plex, host resources) | Roll back client/server to pre-binary-protocol image, compare bandwidth |

---

## What the logging should measure

Add timing instrumentation at these points in the **client** (`pkg/client/client.go`, `handleHTTPRequest`):

```
T1 = start of resp.Body.Read(chunk)
T2 = resp.Body.Read returns
T3 = conn.Send(responseMsg) returns

Segment (T2-T1): Plex read latency per chunk
Segment (T3-T2): tunnel write latency per chunk (includes writeMu wait + frame encode + WebSocket write)
```

And on the **server** (`pkg/server/server.go`, `handleTunnel` read loop / `handleClientRequest`):

```
T4 = conn.Receive() returns a MsgHTTPResponse
T5 = deliverResponse returns (channel send completes)
T6 = w.Write + flusher.Flush return

Segment (T5-T4): delivery latency (0 if channel has room; non-zero = head-of-line blocking)
Segment (T6-T5): HTTP write latency (non-zero = slow downstream browser)
```

Log per-chunk at DEBUG level, with request ID, chunk index, and byte count so individual streams can be correlated.

---

## What to look for in the results

- If **(T3-T2) >> (T2-T1)**: the bottleneck is the tunnel write path — either `writeMu` contention or TCP throughput between client and server host.
- If **(T2-T1)** is high: Plex itself is slow, not the tunnel.
- If **(T5-T4)** is consistently non-zero and growing: the `pending` channel is filling, meaning the HTTP writer (or downstream browser) is the bottleneck.
- If **(T3-T2)** spikes intermittently but averages are fine: classic head-of-line blocking from `writeMu`.

---

## Open questions

- Does contention on `writeMu` visibly spike when multiple streams run concurrently (e.g., video + thumbnail requests)?
- Does the issue reproduce with a single stream (no concurrent requests), which would rule out `writeMu` contention entirely?
- Does increasing `ResponseChunkSize` to 256 KB or 512 KB change the observed throughput?
