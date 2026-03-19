# PlexTunnel Proto

`plex-tunnel-proto` is the shared transport and framing module used by both:

- `https://github.com/CRBL-Technologies/plex-tunnel`
- `https://github.com/CRBL-Technologies/plex-tunnel-server`

It owns the wire-level contract between client and server:

- tunnel message types and validation
- binary frame encoding/decoding
- transport interfaces
- WebSocket transport implementation

## Module

```bash
go get github.com/CRBL-Technologies/plex-tunnel-proto@v1.0.0
```

Import path:

```go
import "github.com/CRBL-Technologies/plex-tunnel-proto/tunnel"
```

## Development

Run the shared protocol test suite:

```bash
go test ./...
```

Run the protocol benchmark suite:

```bash
go test -bench=. ./...
```

For local multi-repo development, create a `go.work` file in the parent directory from either app repo:

```bash
cd ../plex-tunnel-server && make workspace-setup
```
