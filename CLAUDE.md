# CLAUDE.md

## Project

**voicedaemon** — Standalone STT+TTS daemon in Go. Replaces a Python voice daemon.

| Key | Value |
|-----|-------|
| Module | `github.com/realnikolaj/voicedaemon` |
| Go | 1.25+ |
| Entry | `cmd/voicedaemon/` |

## Build Environment

- **libwebrtc-audio-processing-2 IS installed.** Default build uses real APM via cgo.
- **portaudio IS installed** at `/usr/local/Cellar/portaudio/19.7.0`.
- Fallback: build with `-tags noapm` for energy-VAD stub (no webrtc dependency).
- C++ wrapper lives in `internal/capm/` (isolated package to avoid cgo/non-cgo conflicts).

## Quality Gates

Run after every task:

```bash
make check        # Real APM: go build, go test -race, golangci-lint
make check-noapm  # Stub:     go build -tags noapm, go test -race -tags noapm, golangci-lint --build-tags noapm
```

## Package Boundaries

| Package | Owns | Does NOT Own |
|---------|------|--------------|
| `internal/audio/` | portaudio I/O, APM Go wrapper, VAD state machine, resampling | HTTP calls, transcription |
| `internal/capm/`  | WebRTC C++ wrapper, cgo bindings (`apm_wrapper.h/.cpp`) | Go audio logic |
| `internal/stt/` | Speaches STT HTTP client, WAV encoding | Audio capture, VAD |
| `internal/tts/` | TTS streaming clients, job queue, utterance lifecycle | Audio device output |
| `internal/daemon/` | Orchestration, HTTP server, Unix socket server | Raw audio processing |

## YOU MUST NOT

1. Use `log.Printf` — use `logf func(string, ...any)` callback pattern everywhere
2. Discard errors with `_ = ` — log or return every error
3. Use `panic()` or `mustX()` functions — return errors, always
4. Allocate memory in portaudio callbacks — use pre-allocated buffers and channels
5. Hardcode sample rates — use constants from `internal/audio/`
6. Close APM before capture goroutine exits — cancel ctx → WaitGroup → then Close
7. Release speaker lock between buffer copy and stream.Write — hold through both
8. Close channels without mutex protection — use closed flag guard pattern
9. Put C++ source files in `internal/audio/` — they must stay in `internal/capm/` (cgo isolation)

## Go Patterns

- **Error wrapping:** `fmt.Errorf("context: %w", err)`
- **Constructors:** accept `logf` callback, never depend on global logger
- **Audio frames:** 480 samples = 10ms @ 48kHz, use `FrameSize` constant
- **Resampling:** keep in `internal/audio/resample.go`, don't scatter
- **portaudio callbacks:** `select { case ch <- frame: default: }` — never block
- **Shutdown:** context cancel → WaitGroup wait → resource cleanup
- **Table-driven tests:** required for all test functions

## Sample Rates

```
portaudio capture/playback → 48000 Hz
APM processing             → 48000 Hz
Speaches STT input         → 16000 Hz
Speaches TTS (Kokoro)      → 24000 Hz
Speaches TTS (Piper)       → 22050 Hz
PocketTTS output           → 24000 Hz
```

## Commands

```bash
make build        # Build binary (real APM)
make build-noapm  # Build binary (stub, no webrtc dependency)
make test         # Test with race detector (real APM)
make test-noapm   # Test with race detector (stub)
make lint         # golangci-lint (real APM)
make lint-noapm   # golangci-lint (stub)
make check        # build + test + lint (real APM)
make check-noapm  # build + test + lint (stub)
make smoke        # Build stub + smoke test script
```
