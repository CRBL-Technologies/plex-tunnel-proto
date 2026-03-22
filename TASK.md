# TASK: Upgrade Go from 1.22 to 1.25

## Overview

Upgrade Go toolchain to fix known stdlib vulnerabilities. Audit finding 03-infra F1.

---

## Change 1: Update go.mod

### Where to work
- `go.mod` — line 3

### Current behavior
```
go 1.22
```

### Desired behavior
```
go 1.25
```

Run `go mod tidy` after updating.

---

## Constraints
- DO NOT delete or modify any existing code that is not explicitly mentioned in this task.
- Keep all existing tests passing.
- Run `go mod tidy` after changing go.mod.

## Verification
```bash
go build ./...
go test ./...
```

## When Done
Commit all changes with a descriptive commit message. Do NOT modify TASK.md.
