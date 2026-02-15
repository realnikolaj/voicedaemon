# Task: VD-3.0 — Public Go Client Package + Integration Docs

## Status
Complete

## What Was Done
- Created `pkg/vdclient/` — zero-dependency Go client for voicedaemon
- HTTP client: `New()`, `Speak()`, `Stop()`, `Health()` with `context.Context` support
- Socket client: `Dial()`, `Start()`, `Stop()`, `Cancel()`, `Status()`, `Transcripts()` channel, `Close()`
- Shared types matching daemon's actual JSON response shapes
- 11 tests (6 HTTP, 5 socket) all passing with race detector
- Runnable examples: `ExampleClient_Speak`, `ExampleClient_Health`, `ExampleDial`
- Consumer integration guide at `docs/integration.md`

## Files Created
- `pkg/vdclient/types.go` — SpeakRequest, SpeakResponse, HealthResponse, StopResponse, ErrorResponse
- `pkg/vdclient/client.go` — HTTP client (New, Speak, Stop, Health)
- `pkg/vdclient/socket.go` — Unix socket STT session client
- `pkg/vdclient/client_test.go` — 6 HTTP client tests with httptest
- `pkg/vdclient/socket_test.go` — 5 socket tests with fake daemon
- `pkg/vdclient/example_test.go` — runnable examples
- `docs/integration.md` — Go quick start, any-language quick start, API reference, common patterns

## Files NOT Modified
- Everything in `internal/` — untouched
- `cmd/voicedaemon/` — untouched
- `Makefile` — existing `go test ./...` already picks up `pkg/`

## Verification
```bash
make check-noapm   # ✓ build + test + lint (0 issues)
make check          # ✓ build + test + lint (0 issues)
go test -race ./pkg/vdclient/...  # ✓ 1.156s
go vet ./pkg/vdclient/...         # ✓ clean
```

## Notes
- Zero external dependencies in pkg/vdclient — consumers don't inherit voicedaemon's dep tree
- Socket `Transcripts()` channel pattern: `Start()` launches a background goroutine that reads lines from the socket, routing `transcript:` prefixed lines to the channel and capturing the stop/cancel response separately
- Response types match daemon's actual JSON output (verified against `internal/daemon/http.go`)
