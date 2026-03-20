# Transport Layer Validation Contract (v1.0.0+)

## Summary

`decodeMessagePayload` and `WebSocketConnection.Receive()` do **not** call `msg.Validate()`. This is intentional and correct. The transport layer decodes whatever bytes it receives; application-level validation is the caller's responsibility.

The regression tests added in this branch (`TestDecodeMessagePayload_AllowsHTTPRequestContinuationChunk`, `TestWebSocketConnection_ReceiveAllowsHTTPRequestContinuationChunk`) protect this contract from accidental regression.

---

## Background

The old local `pkg/tunnel` implementations (in both `plex-tunnel` and `plex-tunnel-server` before their migration to this module) called `msg.Validate()` inside `decodeMessagePayload`. This was incorrect. The streaming protocol intentionally sends `MsgHTTPRequest` continuation chunks without `Method` or `Path` — only the first chunk carries them. Because the old `Validate()` required both fields unconditionally on every `MsgHTTPRequest`, any POST/PUT request with a body larger than 64 KB would cause `decodeMessagePayload` to return an error at the client, which terminated the entire tunnel session.

This proto module was written with the correct design from the start: the transport decodes, the application validates.

---

## The contract

| Layer | Responsibility |
|---|---|
| `decodeMessagePayload` / `Receive()` | Decode the wire format faithfully. Return an error only for malformed frames (bad length prefix, JSON syntax error, frame type mismatch). |
| Application code | Call `msg.Validate()` explicitly when it needs to enforce message semantics. |

### Example: correct application-layer validation

```go
msg, err := conn.Receive()
if err != nil {
    // wire-level error: bad frame, connection closed, timeout
    return err
}
if err := msg.Validate(); err != nil {
    // semantic error: missing required field for this message type
    // application decides whether to log, skip, or close
    continue
}
```

### Continuation chunks

A `MsgHTTPRequest` continuation chunk is a legitimate wire message:

```go
// Valid to send and receive — carries only ID and body, no Method/Path
tunnel.Message{
    Type: tunnel.MsgHTTPRequest,
    ID:   "req-abc",
    Body: []byte("..."),
}
```

`Receive()` returns it without error. `msg.Validate()` correctly rejects it at the application layer (Method and Path are missing). The application must decide what to do — typically correlating it with an in-flight request by ID, not re-validating it as a standalone message.

---

## What the regression tests verify

`TestDecodeMessagePayload_AllowsHTTPRequestContinuationChunk` and `TestWebSocketConnection_ReceiveAllowsHTTPRequestContinuationChunk` assert two things:

1. `decodeMessagePayload` / `Receive()` does **not** return an error for a continuation chunk — the transport accepts it.
2. `msg.Validate()` **does** return an error for the same chunk — the application layer correctly identifies it as semantically incomplete.

If someone accidentally adds `Validate()` back into the decode path, both tests fail immediately.
