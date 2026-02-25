# TASK-VD-4.0

**Status:** Complete
**Date:** 2026-02-23
**Sprint:** VD-4.0 — Control Interface

## What Was Done
Added runtime audio control endpoints to the HTTP server. Three POST routes (VAD threshold, mic mute, gain) and one GET /config. Extended the public vdclient package with matching wrapper methods. Threaded the audio pipeline reference into the HTTP server struct — this was the critical wiring that previously did not exist.

## Files

| File | Change |
|------|--------|
| `internal/audio/apm_noapm.go` | Modified — VAD threshold now mutable (sync.Mutex-protected float64, SetVADThreshold/VADThreshold methods) |
| `internal/audio/apm.go` | Modified — stub SetVADThreshold/VADThreshold for real APM build (no-op, WebRTC owns its VAD) |
| `internal/audio/pipeline.go` | Modified — added mute (atomic.Bool), gain (mutex-protected float64), threshold passthrough. captureLoop applies mute/gain before APM processing |
| `internal/daemon/http.go` | Modified — added AudioPipeline interface, 4 new handlers (handleSetVADThreshold, handleSetMicMute, handleSetGain, handleConfig), threaded pipeline into HTTPServer struct |
| `internal/daemon/daemon.go` | Modified — NewHTTPServer call now passes pipeline reference |
| `internal/daemon/socket.go` | Modified — removed unused AudioPipeline/VadStateString/Transcriber interface declarations (socket uses callbacks, not interfaces) |
| `internal/daemon/http_test.go` | Modified — added mockPipeline, updated startTestHTTPServer to return mock, added TestHTTPSetVADThreshold/TestHTTPSetMicMute/TestHTTPSetGain/TestHTTPConfig |
| `pkg/vdclient/types.go` | Modified — added ConfigResponse struct |
| `pkg/vdclient/client.go` | Modified — added SetVADThreshold, SetMicMute, SetGain, Config methods |
| `pkg/vdclient/client_test.go` | Modified — added TestSetVADThreshold, TestSetMicMute, TestSetGain, TestConfig |

## Verification
```
make check-noapm → PASS (build, test -race, golangci-lint: 0 issues)
```

## Boundary Changes

| Endpoint | Method | Request | Response |
|----------|--------|---------|----------|
| `/vad/threshold` | POST | `{"threshold": float64}` | `{"status":"ok","threshold":N}` |
| `/mic/mute` | POST | `{"muted": bool}` | `{"status":"ok","muted":B}` |
| `/gain` | POST | `{"gain": float64}` | `{"status":"ok","gain":N}` |
| `/config` | GET | — | `{"vad_threshold":N,"muted":B,"gain":N,"speaches_url":"...","pocket_tts_url":"...","stt_url":"...","port":N}` |

New vdclient methods: `SetVADThreshold(ctx, float64)`, `SetMicMute(ctx, bool)`, `SetGain(ctx, float64)`, `Config(ctx) (*ConfigResponse, error)`

## Notes
- Validation: threshold and gain must be > 0. Mute accepts any bool.
- Real APM build: SetVADThreshold logs a warning and no-ops (WebRTC APM owns its VAD internally).
- Removed 3 unused interface declarations from socket.go (AudioPipeline, VadStateString, Transcriber) — socket server uses callbacks, never referenced these types.
