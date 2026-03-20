# Repository Guide

This repository contains the shared PlexTunnel transport and framing module.

## Main Files

- `README.md`: module purpose, import path, and basic usage
- `tunnel/`: shared protocol and transport implementation
- `reviews/`: review template only
- `REPO_STRUCTURE.md`: broader three-repo planning and rollout notes

## Workflow

1. Put public module usage in `README.md`.
2. Keep transport, framing, and validation changes in `tunnel/`.
3. Use `reviews/TEMPLATE.md` for temporary review writeups, then remove the concrete review file once the work is merged or reflected in the main docs.
4. When client/server behavior changes require coordination, update the shared docs here and the consuming repo docs as needed.

## Relationship To Other Repos

This module is consumed by:

- `github.com/CRBL-Technologies/plex-tunnel`
- `github.com/CRBL-Technologies/plex-tunnel-server`

For local multi-repo work, create a parent-level `go.work` from one of the app repos:

```bash
cd ../plex-tunnel-server && make workspace-setup
```
