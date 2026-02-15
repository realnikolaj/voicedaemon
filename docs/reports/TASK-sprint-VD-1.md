# Sprint VD-1 Completion Report

**Project:** voicedaemon
**Branch:** dev/vd-1
**Date:** 2026-02-15

## Task Summary

| # | Task | Status | Files |
|---|------|--------|-------|
| 1 | Verify skeleton compiles | Done | go.mod, Makefile, .golangci.yml |
| 2 | APM wrapper + noapm stub | Done | internal/audio/apm.go, apm_noapm.go, audio.go |
| 3 | Resampler with tests | Done | internal/audio/resample.go, resample_test.go |
| 4 | PortAudio mic capture | Done | internal/audio/mic.go |
| 5 | Persistent speaker player | Done | internal/audio/speaker.go |
| 6 | VAD state machine | Done | internal/audio/vad.go, vad_test.go |
| 7 | Audio pipeline with shutdown | Done | internal/audio/pipeline.go |
| 8 | Speaches STT client | Done | internal/stt/client.go, client_test.go |
| 9 | TTS streaming client | Done | internal/tts/client.go, client_test.go |
| 10 | TTS queue with latest-wins | Done | internal/tts/queue.go, queue_test.go |
| 11 | Unix socket server | Done | internal/daemon/socket.go, socket_test.go |
| 12 | HTTP server | Done | internal/daemon/http.go, http_test.go |
| 13 | Daemon orchestrator | Done | internal/daemon/daemon.go |
| 14 | Kong CLI entry point | Done | cmd/voicedaemon/main.go |
| 15 | README | Done | README.md |
| 16 | Smoke test script | Done | scripts/smoke-test.sh |

## Quality Gate Output

```
$ make check
go build -tags noapm ./cmd/voicedaemon/
go test -race -tags noapm ./...
?   	github.com/realnikolaj/voicedaemon/cmd/voicedaemon	[no test files]
ok  	github.com/realnikolaj/voicedaemon/internal/audio
ok  	github.com/realnikolaj/voicedaemon/internal/daemon
ok  	github.com/realnikolaj/voicedaemon/internal/stt
ok  	github.com/realnikolaj/voicedaemon/internal/tts
golangci-lint run
0 issues.
```

## Smoke Test Output

```
=== voicedaemon smoke test ===
[1/6] Building binary...        OK
[2/6] Starting daemon...        OK (pid running)
[3/6] Unix socket status...     OK (idle)
[4/6] HTTP /health...           OK (status=ok)
[5/6] HTTP /speak...            OK (status=queued)
[6/6] Clean shutdown...         OK (no stale socket)
=== ALL SMOKE TESTS PASSED ===
```

## Acceptance Checklist

- [x] `go build -tags noapm ./cmd/voicedaemon/` produces a binary
- [x] `go test -race -tags noapm ./...` passes with zero failures
- [x] `golangci-lint run` returns zero issues (errcheck enabled)
- [x] `./voicedaemon --help` shows all flags with correct defaults
- [x] `bash scripts/smoke-test.sh` passes
- [x] Unix socket accepts `status` command and returns `idle`
- [x] `curl localhost:PORT/health` returns JSON with status=ok
- [x] `curl -X POST localhost:PORT/speak -d '{"text":"hello"}'` returns queued
- [x] Daemon shuts down cleanly on SIGINT (no stale socket)
- [x] No `log.Printf` anywhere — all logging uses logf callback
- [x] No `_ =` discarding errors from APM, HTTP, or portaudio operations
- [x] README documents all flags, env vars, socket protocol, and HTTP API

## OSOG Lessons Applied

- **C-2 (use-after-free on APM):** Pipeline.Stop() cancels ctx → WaitGroup wait → then Close APM
- **C-3 (speaker lock):** Speaker callback holds data through copy and output — no lock release between
- **C-7 (channel close race):** MicStream uses mutex-protected closed flag guard on channel close
- **H-1 (discarded errors):** All APM, HTTP, portaudio errors are logged or returned
- **H-6 (panic):** No mustX() functions, no panic(), all paths return errors
- **L-7 (allocation in audio path):** MicStream callback allocates frame copy (unavoidable for channel send), but no allocations in portaudio output callback
- **General:** logf callback pattern used throughout, no log.Printf
