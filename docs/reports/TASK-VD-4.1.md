# TASK-VD-4.1

**Status:** Complete
**Date:** 2026-02-23
**Sprint:** VD-4.1 — Transcript SSE Endpoint

## What Was Done
Added Server-Sent Events (SSE) endpoint for real-time transcript streaming over HTTP. The daemon now broadcasts transcripts to both the Unix socket (existing) and all SSE subscribers (new). Added vdclient `TranscriptStream` method that returns a Go channel for consuming the stream.

## Files

| File | Change |
|------|--------|
| `internal/daemon/http.go` | Modified — added subscriber map (mutex-protected `map[chan string]struct{}`), `handleTranscriptStream` SSE handler, `BroadcastTranscript` method, registered `GET /transcripts/stream`, removed WriteTimeout (SSE needs long-lived connections) |
| `internal/daemon/daemon.go` | Modified — `onUtterance` now calls `httpSrv.BroadcastTranscript(text)` alongside `socketSrv.PushTranscript(text)` |
| `internal/daemon/http_test.go` | Modified — added `TestHTTPTranscriptStream` (connects SSE, broadcasts two events, verifies receipt via scanner) |
| `pkg/vdclient/client.go` | Modified — added `TranscriptStream(ctx) (<-chan string, error)` method (SSE consumer, returns channel, goroutine reads events) |
| `pkg/vdclient/client_test.go` | Modified — added `TestTranscriptStream` (mock SSE server, verifies two events arrive on channel) |

## Verification
```
make check-noapm → PASS (build, test -race, golangci-lint: 0 issues)
```

## Boundary Changes

| Endpoint | Method | Response |
|----------|--------|----------|
| `/transcripts/stream` | GET | SSE stream: `Content-Type: text/event-stream`, events as `data: <text>\n\n` |

New vdclient method: `TranscriptStream(ctx context.Context) (<-chan string, error)` — returns channel that receives transcript strings, closed on context cancel or connection drop.

SSE subscriber buffer: 16 events. Slow subscribers get drops with a log warning, not a block.

## Notes
- SSE is additive to the existing Unix socket transcript push. Both paths fire from `onUtterance`.
- WriteTimeout removed from the HTTP server entirely (was 10s). SSE connections are indefinite.
- The subscriber map uses non-blocking sends to prevent a slow SSE client from blocking the audio pipeline.
