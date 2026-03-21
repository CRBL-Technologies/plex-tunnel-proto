# Task: Rebrand to Portless + switch to Elastic License 2.0

## 1. Replace LICENSE file

Replace the entire contents of `LICENSE` with the Elastic License 2.0 text. Copy it exactly from `/home/dev/github/plex-tunnel-server/LICENSE`.

## 2. Rebrand user-facing docs

In these files, replace "PlexTunnel" with "Portless" in **user-facing text only**:

- `README.md` — update title and description
- `REPO_GUIDE.md` — update description

**DO NOT modify:**
- Go source code, imports, or module paths
- `REPO_STRUCTURE.md` (historical design spec)
- Any `.go` files

## Verification

```bash
# Confirm LICENSE was replaced:
head -1 LICENSE
# Should output: ## Elastic License 2.0 (ELv2)

# Confirm README title:
head -1 README.md
# Should output: # Portless Proto

# Confirm no Go files were modified:
git diff --name-only -- '*.go'
# Should output nothing
```
