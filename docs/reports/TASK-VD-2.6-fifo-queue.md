# Task: VD-2.6 — FIFO queue serialization

## Status
Complete

## What Was Done
- Removed latest-wins drain+abort from `Enqueue()` — it now does a simple FIFO channel push
- `StopPlayback()` retained for explicit cancellation (abort current + drain pending)
- Added "processing job" and "job complete" log lines with text truncated to 30 chars
- Replaced `TestQueueLatestWins` with `TestQueueFIFO` — verifies both jobs process in order, both get Begin+End calls

## Root Cause
`Enqueue()` implemented latest-wins: every new job drained pending jobs and called `StopUtterance()` on the current one. With two rapid enqueues, the second job aborted the first before it played. The worker loop itself was already synchronous — the abort semantics in `Enqueue` were the problem.

## Files Modified
- `internal/tts/queue.go`: `Enqueue()` simplified to FIFO push; `processJob()` adds "processing job" / "job complete" logs
- `internal/tts/queue_test.go`: `TestQueueLatestWins` → `TestQueueFIFO`; removed unused `sync/atomic` import

## Verification
```bash
make check-noapm   # ✓ 0 issues
make check          # ✓ 0 issues
```

## Notes
- Explicit cancellation still works via `StopPlayback()` (used by socket "cancel" command and HTTP stop endpoint)
- `curAbort` context per-job is retained so `StopPlayback()` can cancel an in-flight HTTP stream
