# ADR: WebSocket multiplexing semantics & per-stream flow control

## Context

PR #253 on `plex-tunnel-server` (merged 2026-04-17 11:08:53Z, reverted 13:27:41Z) attempted to route `/:/websockets/notifications` as control-class by honoring the classifier in `handleWebSocketPassthrough`. The change moved the stream from leasing 1-of-10 data tunnels onto the singleton control WebSocket. That regressed every other control request because there is no flow control between concurrent streams sharing a single WebSocket — one stream's writes serialize all others (head-of-line blocking).

The `MsgWSOpen` / `MsgWSFrame` / `MsgWSClose` frame types were added in `ProtocolVersion = 2` (see `tunnel/message.go:20-26`). What is missing — and what caused the regression — is a documented multiplexing model and per-stream flow control. Issue `plex-tunnel#92` tracks the client-side implementation; it is blocked on this ADR because both sides need to agree on flow-control semantics before they diverge in implementation.

## Goal

Write an ADR in `docs/adr/` that documents the chosen multiplexing semantics and specifies the new flow-control frame (`MsgWSWindowUpdate`). This PR contains the ADR ONLY — no code changes to `tunnel/*.go`. The actual protocol implementation lands in a follow-up PR gated on the CEO ratifying the ADR.

## Constraints / guardrails

- **DO NOT delete or modify any code or file not explicitly listed in the Tasks section below.**
- DO NOT modify `tunnel/message.go`, `tunnel/frame.go`, or any other `.go` file. This is a docs-only PR.
- DO NOT invent a new channel class or topology change. The decision is: flow control on the existing control WebSocket.
- DO NOT propose numeric even/odd stream-id allocation. Existing string-UUID `ID` field stays — document the rationale, don't redesign it.
- Use the `/architecture-decision-records` skill to structure the document. Follow whatever ADR template that skill produces (typically Context / Decision / Consequences / Alternatives).

## Tasks

- [ ] **Create `docs/adr/` directory** (does not exist in plex-tunnel-proto today — this ADR establishes the convention).

- [ ] **Create `docs/adr/0001-websocket-multiplexing-flow-control.md`** using the `/architecture-decision-records` skill. Number is `0001` because this is the first ADR in the repo.

- [ ] **Content sections (adapt to whatever structure the skill produces, but these decisions MUST be present):**

  1. **Problem statement** — HOL blocking on the shared control WebSocket when one stream dominates writes. Cite PR #253 regression (server commit `a1c23ea`, reverted `f3cc53f`).
  2. **Current state** — `MsgWSOpen` / `MsgWSFrame` / `MsgWSClose` already defined at `ProtocolVersion = 2` in `tunnel/message.go:20-26`. Frames use string-UUID stream IDs via the existing `Message.ID` field, validated in `Validate()` at lines 111-124. What is undefined today: concurrency semantics when multiple streams share one WebSocket, backpressure policy, behavior when a receiver cannot keep up.
  3. **Decision** —
     - Per-stream credit-based flow control (HTTP/2 WINDOW_UPDATE inspired).
     - **New frame: `MsgWSWindowUpdate`** (next unused type value, currently would be 14 — codex MUST verify against `tunnel/message.go` when drafting). Carries a stream `ID` plus a non-negative delta representing additional credit granted by the receiver to the sender. Recommend adding a `WindowDelta uint32` or `WindowIncrement uint32` field to `Message` / `frameMetadata` (name choice is codex's call — pick whichever reads better in the Go API; document the chosen name in the ADR). Zero or negative deltas must be a protocol error.
     - **Default initial window:** 64 KiB per stream. Negotiable upward in the future via a separate SETTINGS-style frame; this ADR does not introduce one — the initial window is a fixed protocol constant until a follow-up ADR revisits.
     - **Credit accounting:** sender of `MsgWSFrame` decrements its local credit by `len(Body)` before sending. Receiver decrements its read-side counter as it delivers bytes to the consumer; sends `MsgWSWindowUpdate` when consumed bytes cross a configurable threshold (recommend when at least half the initial window has been consumed, to limit chatter). If a sender would have to exceed its current credit, it must block (do not drop frames).
     - **Protocol-violation behavior:** if sender exceeds credit, the receiver SHOULD send `MsgError` with an explanatory reason and close the offending stream with `MsgWSClose` status `1008` (policy violation). Do NOT tear down the whole tunnel for a single-stream flow-control violation.
     - **Stream IDs:** unchanged — existing string UUID via `Message.ID`. State this explicitly as a non-decision (rationale: UUIDs already provide zero-coordination stream creation, no value in migrating to numeric even/odd).
     - **Multiplex channel:** on the existing control WebSocket. No new channel class.
     - **Protocol version bump:** `ProtocolVersion` raised from 2 → 3. Describe the compatibility rule in the Decision: during Register/RegisterAck handshake, if either peer advertises only v2, both sides fall back to the current behavior (no flow control — the stream either rides the data-lease pool or a single stream at a time on control, mirroring today). Neither side emits `MsgWSWindowUpdate` on a v2-negotiated session.
     - **Frame metadata JSON field:** whichever field name codex picks for the delta, add it to both `Message` and `frameMetadata` — note this future code change is required but NOT done in this PR.

  4. **Alternatives considered — document and explicitly reject:**
     - **Separate WS-mux channel class** (a third class between control singleton and leased data pool, dedicated to WS multiplexing). Reject: adds topology complexity for no functional gain over per-stream flow control on the existing control WS. The root cause is per-stream starvation, not tunnel identity. Also conflicts with the architectural goal of "everything through 1 tunnel except streaming/downloading".
     - **Piggyback credit information on `MsgWSFrame`** (e.g. add a `credit_ack` field to existing frame). Reject: couples credit replenishment to data flow — a stream that has nothing to send back has no way to signal receiver-side consumption to the sender. Dedicated `MsgWSWindowUpdate` is the HTTP/2 precedent and cleanly decouples.
     - **Numeric even/odd stream IDs (HTTP/2-style).** Reject: string UUIDs already solve zero-coordination stream creation. Switching would churn the existing `ID` field used for HTTP request streams too, with no compensating benefit.

  5. **Consequences** —
     - Positive: HOL blocking eliminated; `/:/websockets/notifications` and other WS control streams can coexist without starving library/metadata/SSE.
     - Positive: additive protocol change; v2 peers keep working with current semantics.
     - Negative: proto version bump needs coordinated client + server rollout. Old clients will not benefit from flow control until upgraded.
     - Negative: receiver-side credit tracking adds per-stream state; bound is O(streams × 8 bytes) — negligible.
     - Neutral: window size of 64 KiB may need tuning; a follow-up ADR can introduce a SETTINGS frame if we see tuning pressure.

  6. **Follow-up work (reference only — NOT part of this PR):**
     - `plex-tunnel-proto`: add `MsgWSWindowUpdate` constant, field on `Message`/`frameMetadata`, validator branch, tests. Bump `ProtocolVersion` to 3.
     - `plex-tunnel-server`: multiplex router that emits/honors credits on control-WS WebSocket streams.
     - `plex-tunnel` (issue `#92`): client-side WS demux, credit accounting, bridge to local Plex WS.

- [ ] **Do not touch any existing file under `tunnel/`.** If the skill suggests adding a CHANGELOG entry or updating README, skip it for this PR — the ADR stands alone.

## Tests

None. Docs-only PR; no code changes.

## Acceptance criteria

1. `docs/adr/0001-websocket-multiplexing-flow-control.md` exists and contains all six sections above (problem, current state, decision, alternatives, consequences, follow-up).
2. The Decision section names the new frame (`MsgWSWindowUpdate`), the chosen field name for the credit delta, the default initial window (64 KiB), the threshold for emitting a window update (half the window), the protocol-version bump (2 → 3), and the backward-compat fallback rule.
3. The Alternatives section explicitly rejects: separate channel class, piggybacked credits on `MsgWSFrame`, numeric even/odd stream IDs — each with a one- to two-sentence reason.
4. No `.go` file in the repo is modified. `git diff HEAD --stat` shows only the new ADR file.
5. The ADR references real file/line locations in `tunnel/message.go` and `tunnel/frame.go` so reviewers can cross-check.

## Verification

```bash
cd /home/dev/worktrees/paul/plex-tunnel-proto
git diff HEAD --stat                     # must show only docs/adr/0001-*.md
go build ./...                            # sanity check — should still pass since no code changed
go vet ./...
```

No tests to run.
