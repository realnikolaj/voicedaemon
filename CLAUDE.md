# CLAUDE.md

## Project

**voicedaemon** — Standalone STT+TTS daemon in Go. Replaces a Python voice daemon.

| Key | Value |
|-----|-------|
| Module | `github.com/realnikolaj/voicedaemon` |
| Go | 1.25+ |
| Entry | `cmd/voicedaemon/` |

## Build Environment

- **libwebrtc-audio-processing is NOT installed.** All builds MUST use `-tags noapm`.
- **portaudio IS installed** at `/usr/local/Cellar/portaudio/19.7.0`.
- Do not attempt to fix or debug webrtc-audio-processing cgo linking.

## Quality Gates

Run after every task:

```bash
make check
# Expands to: go build -tags noapm, go test -race -tags noapm, golangci-lint run
```

## Package Boundaries

| Package | Owns | Does NOT Own |
|---------|------|--------------|
| `internal/audio/` | portaudio I/O, APM wrapper, VAD state machine, resampling | HTTP calls, transcription |
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
9. Build without `-tags noapm` — webrtc-audio-processing is not available

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
make build    # Build binary (-tags noapm)
make test     # Test with race detector
make lint     # golangci-lint
make check    # All of the above
make smoke    # Build + smoke test script
```
