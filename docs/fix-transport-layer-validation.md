# Fix: Remove Validate() from Transport Layer (v1.1.0)

## Problem

`decodeMessagePayload` in `tunnel/frame.go` calls `msg.Validate()` after decoding every received message. This is wrong: validation is an application-layer concern, not a transport-layer one. The transport layer's job is to faithfully decode whatever bytes it receives; it is not responsible for enforcing message semantics.

This causes a real regression in the server/client migration to this proto module.

### Failure scenario

The server streams large HTTP request bodies to the client in chunks (`MsgHTTPRequest`). The streaming protocol sends method/path/headers only on the **first** chunk; continuation chunks carry only `ID` + `Body`:

```
chunk 1: {Type: MsgHTTPRequest, ID: "x", Method: "POST", Path: "/...", Headers: {...}, Body: [...64KB...]}
chunk 2: {Type: MsgHTTPRequest, ID: "x", Body: [...64KB...]}   ← no Method, no Path
chunk 3: {Type: MsgHTTPRequest, ID: "x", Body: [...], EndStream: true}
```

Because `Validate()` requires `Method != ""` and `Path != ""` for every `MsgHTTPRequest`, the client's `conn.Receive()` returns an error on chunk 2. The client treats any receive error as fatal and terminates the entire session, dropping all in-flight requests and triggering a reconnect with backoff.

For GET requests (most Plex streaming), there is no request body so only one chunk is ever sent and it always passes validation. This is why the issue shows up as slow or intermittent bandwidth rather than a total outage — it only fires on POST/PUT requests with bodies larger than 64 KB (e.g. Plex sync, playlist creation, large XML payloads).

### Why Validate() does not belong here

`Validate()` enforces application-level invariants: required fields, valid field combinations. These rules are defined by the application protocol, not the wire format. The wire format (frame header + JSON metadata + binary body) is self-describing and does not require semantic validation to be decoded.

Putting `Validate()` in the decode path creates tight coupling between the transport layer and application semantics. Any future protocol extension that adds a new message type or relaxes a field requirement would break all callers silently at the receive layer rather than visibly at the application layer.

---

## Fix

### 1. Remove `Validate()` from `decodeMessagePayload`

**`tunnel/frame.go`**

```go
// Before
func decodeMessagePayload(payload []byte) (Message, error) {
    frame, err := UnmarshalFrame(payload)
    if err != nil {
        return Message{}, fmt.Errorf("decode frame: %w", err)
    }
    msg, err := frame.Message()
    if err != nil {
        return Message{}, fmt.Errorf("decode frame message: %w", err)
    }
    // Remove this:
    if err := msg.Validate(); err != nil {
        return Message{}, fmt.Errorf("validate message: %w", err)
    }
    return msg, nil
}

// After
func decodeMessagePayload(payload []byte) (Message, error) {
    frame, err := UnmarshalFrame(payload)
    if err != nil {
        return Message{}, fmt.Errorf("decode frame: %w", err)
    }
    msg, err := frame.Message()
    if err != nil {
        return Message{}, fmt.Errorf("decode frame message: %w", err)
    }
    return msg, nil
}
```

### 2. Callers must validate themselves

Applications that need to validate received messages should call `msg.Validate()` themselves after receiving:

```go
msg, err := conn.Receive()
if err != nil {
    // handle
}
if err := msg.Validate(); err != nil {
    // handle invalid message — log, skip, or close connection
    // depending on application policy
}
```

The server already does this (`server.go` line 300). The client does not need to validate received messages in the streaming path — it trusts the server.

### 3. Remove the redundant Validate() call in the server

Once the proto no longer validates on decode, the explicit `msg.Validate()` call in the server's tunnel receive loop becomes the sole validation point — which is correct. No change needed in the server; it already validates explicitly.

---

## Release

Tag as **v1.1.0** (minor bump — the removal of `Validate()` from the decode path is a behavioral change but not an API-breaking one; `Validate()` remains exported and callable by application code).

**Changelog entry:**

```
v1.1.0
- fix: remove Validate() from decodeMessagePayload — validation is an
  application-layer concern; the transport layer now faithfully decodes
  all frames regardless of field completeness. Callers that need
  validation should call msg.Validate() explicitly after Receive().
```

---

## Short-term workaround (server, until v1.1.0 is released)

In `pkg/server/server.go`, `sendHTTPRequestStream`: always include `Method` and `Path` in every request chunk, not just the first. Skip `Headers` on continuation chunks to avoid bloat:

```go
msg := tunnel.Message{
    Type:      tunnel.MsgHTTPRequest,
    ID:        requestID,
    Method:    method,
    Path:      requestPath,
    Body:      chunk,
    EndStream: finalChunk,
}
if !sentRequestStart {
    msg.Headers = headers
}
```

This ensures all chunks pass the proto's current `Validate()` check. The overhead is `Method` + `Path` in the JSON metadata of each continuation chunk — typically under 100 bytes per chunk, negligible against a 64 KB body.
