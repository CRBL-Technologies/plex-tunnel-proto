# Task: Add MsgMaxConnectionsUpdate message type

## Context

We need a new message type `MsgMaxConnectionsUpdate` that the server can send to clients to dynamically adjust their `max_connections` without requiring a reconnect.

## Changes to tunnel/message.go

1. Add `MsgMaxConnectionsUpdate` after `MsgKeyExchange` in the `const` block:
   ```go
   MsgMaxConnectionsUpdate // server tells client to adjust its connection pool size
   ```

2. Add validation for `MsgMaxConnectionsUpdate` in the `Validate()` switch. It requires:
   - `MaxConnections >= 1` (the new limit must be at least 1)

That's it — the existing `MaxConnections` field on `Message` already exists and can be reused.

## Changes to tunnel/message_test.go

Add test cases for `MsgMaxConnectionsUpdate` validation:
- Valid: `{Type: MsgMaxConnectionsUpdate, MaxConnections: 4}` should pass
- Invalid: `{Type: MsgMaxConnectionsUpdate, MaxConnections: 0}` should fail
- Invalid: `{Type: MsgMaxConnectionsUpdate, MaxConnections: -1}` should fail

## DO NOT modify
- tunnel/codec.go
- tunnel/websocket.go
- Any other files

## Verification
Run `go test -race ./...` — all tests must pass.
