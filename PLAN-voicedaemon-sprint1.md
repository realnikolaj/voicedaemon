# Sprint VD-1: voicedaemon — Go Voice Daemon from Scratch

**Directory:** `~/git/voicedaemon/`
**Module:** `github.com/realnikolaj/voicedaemon`
**Branch:** `main` (greenfield)
**Go version:** 1.25+
**Scope:** Complete STT+TTS daemon replacing the Python `voice_daemon`
**Goal:** Standalone binary with mic capture → go-webrtc-apm → VAD → Speaches STT → transcript push via Unix socket (Hammerspoon compatible) + HTTP TTS API with streaming playback and echo cancellation.

---

## Context for the Agent

You are building a standalone Go daemon from scratch. This is NOT part of the osog repository. It is an independent project at `~/git/voicedaemon/`.

**What this replaces:** A Python daemon (`voice_daemon/`) that provides:
- STT via Unix socket at `/tmp/voice-daemon.sock` (Hammerspoon integration)
- TTS via HTTP API on `:5111` (MCP tool integration)
- Silero VAD for voice activity detection
- Two TTS backends: Speaches + PocketTTS
- Persistent audio stream that stays open between utterances

**What Go adds over Python:**
- go-webrtc-apm replaces Silero VAD (adds noise suppression, AGC, echo cancellation)
- Echo cancellation enables listen-while-speaking (full duplex)
- Single static binary, no Python/venv/torch dependency chain
- portaudio for both mic input and speaker output (needed for AEC symmetry)

**Reference material:**
- go-webrtc-apm API: see `## go-webrtc-apm API` section below
- Speaches API: see `## Speaches API` section below
- Socket protocol: see `## Unix Socket Protocol` section below

---

## Package Structure

```
voicedaemon/
├── cmd/voicedaemon/
│   └── main.go              Kong CLI entry: flags, signal handling, daemon.Run()
├── internal/
│   ├── audio/
│   │   ├── apm.go           go-webrtc-apm wrapper (NS + AGC + VAD + AEC)
│   │   ├── apm_noapm.go     Stub for builds without libwebrtc-audio-processing
│   │   ├── mic.go           portaudio input stream, callback → channel
│   │   ├── speaker.go       portaudio output stream, persistent player pattern
│   │   ├── resample.go      Linear interpolation resampler (48k↔16k↔24k)
│   │   ├── vad.go           VAD state machine (IDLE→LISTENING→RECORDING→PROCESSING)
│   │   └── pipeline.go      Orchestrates mic→APM→VAD→callback, speaker→APM render
│   ├── stt/
│   │   └── client.go        Speaches STT HTTP POST client (WAV encode, transcript return)
│   ├── tts/
│   │   ├── client.go        Unified TTS streaming client (Speaches + PocketTTS)
│   │   └── queue.go         TTS job queue, latest-wins drain, utterance lifecycle
│   └── daemon/
│       ├── daemon.go         Daemon struct: owns pipeline + TTS queue + servers
│       ├── socket.go         Unix socket server (Hammerspoon protocol)
│       └── http.go           HTTP server (:5111) — /speak, /stop, /health
├── go.mod
├── go.sum
├── Makefile
├── .golangci.yml
└── README.md
```

**Package boundaries (strict):**

| Package | Owns | Does NOT Own |
|---------|------|--------------|
| `internal/audio/` | portaudio I/O, go-webrtc-apm lifecycle, VAD state machine, resampling, pre-speech buffer | HTTP calls, transcription, TTS streaming |
| `internal/stt/` | Speaches STT HTTP client, WAV encoding, transcript parsing | Audio capture, VAD, playback |
| `internal/tts/` | TTS HTTP streaming clients, backend selection, job queue, utterance lifecycle | Audio device output (delegates to audio/speaker) |
| `internal/daemon/` | Orchestration, HTTP server, Unix socket server, wiring audio→STT→push, wiring TTS→playback | Raw audio processing, HTTP client details |

---

## Tasks

| # | Task | Files | Call Chain | Verify |
|---|------|-------|------------|--------|
| 1 | **Init module + directory structure.** `go mod init github.com/realnikolaj/voicedaemon`. Create all package directories. Create `Makefile` with `build`, `test`, `lint`, `check` targets. Create `.golangci.yml` with errcheck enabled. Create `go.sum`. | `go.mod`, `Makefile`, `.golangci.yml`, directory tree | N/A | `go mod tidy` clean, `make build` skeleton compiles |
| 2 | **APM wrapper.** Create `apm.go` with build tag `//go:build !noapm`. Wrap `go-webrtc-apm` with OSOG-learned defaults: NS High, VAD Moderate, AEC High+DelayAgnostic, AGC AdaptiveDigital, HighPassFilter on. Export `Processor` struct with `ProcessCapture(frame []float32) ([]float32, bool, error)`, `ProcessRender(frame []float32) error`, `Close()`. Frame size constant: `FrameSize = 480` (10ms @ 48kHz). Create `apm_noapm.go` stub with `//go:build noapm` that returns a no-op Processor (passthrough audio, always `hasVoice=false`). | `internal/audio/apm.go`, `internal/audio/apm_noapm.go` | `NewProcessor(cfg)` → `apm.New(config)` ; `ProcessCapture()` → `apm.ProcessCapture()` | `go build ./...` + `go build -tags noapm ./...` — both compile |
| 3 | **Resampler.** Linear interpolation resampler for speech. `Resample(samples []float32, srcRate, dstRate int) []float32`. Add `ResampleS16LE(data []byte, srcRate, dstRate int) []byte` for PCM byte conversion. Table-driven tests with known sample count ratios: 48000→16000 (3:1), 24000→48000 (1:2), 22050→48000. | `internal/audio/resample.go`, `internal/audio/resample_test.go` | `Resample(samples, 48000, 16000)` → interpolated output | `go test -race ./internal/audio/...` |
| 4 | **Mic capture.** portaudio input stream at 48kHz mono float32. Non-blocking callback writes frames to a channel: `select { case ch <- frame: default: }` (never block audio thread). Pre-allocate frame buffer in callback. Export `MicStream` struct with `Start()`, `Stop()`, `Frames() <-chan []float32`. | `internal/audio/mic.go`, `internal/audio/mic_test.go` | `NewMicStream(cfg)` → `Start()` → callback pushes to `Frames()` channel | `go build ./...` (portaudio link test) |
| 5 | **Speaker output (persistent player pattern).** portaudio output stream at 48kHz mono int16. Stays open between utterances. `BeginUtterance()` resets state. `Feed(chunk []byte, srcRate int)` resamples to 48kHz and enqueues. `EndUtterance()` signals EOS. `WaitUtterance(timeout)` blocks until playback done. `StopUtterance()` aborts immediately. Uses chunk queue (capacity 200). Callback pulls from queue, pads with silence when empty. | `internal/audio/speaker.go`, `internal/audio/speaker_test.go` | `NewSpeaker(cfg)` → `Open()` → `BeginUtterance()` → `Feed()` → `EndUtterance()` → `WaitUtterance()` | `go build ./...` |
| 6 | **VAD state machine.** States: IDLE, LISTENING, RECORDING, PROCESSING. Pre-buffer: circular buffer of 25 frames (~800ms at 32ms/frame). Silence gap: 20 consecutive non-voice frames (~640ms). On state transition RECORDING→PROCESSING: concatenate pre-buffer + recorded frames, call `onUtterance(audio []float32)` callback. Audio callback always captures regardless of state. Thread-safe state transitions with mutex. Export `State() VadState` method. | `internal/audio/vad.go`, `internal/audio/vad_test.go` | `NewVADMachine(cfg, onUtterance)` → `ProcessFrame(samples []float32, hasVoice bool)` → state transitions → `onUtterance(audio)` | `go test -race -run TestVADStateMachine ./internal/audio/...` — test: IDLE→LISTENING→start, voice frames→RECORDING, silence→PROCESSING→callback fired |
| 7 | **Audio pipeline.** Orchestrates mic→APM→VAD. Owns `Processor` (APM), `MicStream`, `Speaker`. Capture goroutine reads from `MicStream.Frames()`, calls `ProcessCapture`, feeds result + hasVoice to VAD. Render path: `FeedRender(samples []float32)` calls `ProcessRender` for AEC reference before speaker output. **Correct shutdown:** cancel context → WaitGroup on capture goroutine → then Close APM (lesson from osog C-2). **Correct speaker locking:** hold lock through buffer write + stream.Write (lesson from osog C-3). | `internal/audio/pipeline.go`, `internal/audio/pipeline_test.go` | `NewPipeline(cfg)` → `Start(onUtterance)` → capture goroutine → `Stop()` waits goroutine → closes APM | `go test -race ./internal/audio/...` |
| 8 | **STT client.** Pure HTTP POST to Speaches `/v1/audio/transcriptions`. Accepts `[]float32` at 48kHz, resamples to 16kHz, encodes WAV, POSTs multipart form. Returns transcript string. Configurable URL, model, language. Normalizes audio before encoding (peak normalize). Timeout 30s. Error wrapping. | `internal/stt/client.go`, `internal/stt/client_test.go` | `NewClient(cfg)` → `Transcribe(ctx, audio []float32) (string, error)` → resample → WAV encode → POST → parse JSON → return text | `go test -race ./internal/stt/...` (test with mock HTTP server) |
| 9 | **TTS streaming client.** Two backends: Speaches and PocketTTS. Both stream raw PCM s16le via `POST /v1/audio/speech` with `response_format: pcm`. Unified interface: `Stream(ctx, text, backend, opts) (<-chan []byte, error)` returns channel of PCM chunks. Configurable URLs, models, voices, sample rates. Known model→sample rate lookup table (Kokoro: 24000, Piper/GLaDOS: 22050, PocketTTS: 24000). | `internal/tts/client.go`, `internal/tts/client_test.go` | `NewClient(cfg)` → `Stream(ctx, "hello", BackendSpeaches, nil)` → HTTP stream → chunks on channel | `go test -race ./internal/tts/...` (test with mock HTTP server) |
| 10 | **TTS queue.** Job queue with latest-wins semantics: new response drains old queue and cancels current playback (Python pattern: `worker.py`). Owns reference to TTS client and Speaker. `Enqueue(job)` adds to async queue. Worker goroutine: dequeue → `speaker.BeginUtterance()` → stream TTS chunks → `speaker.Feed()` each chunk → `speaker.EndUtterance()` → `speaker.WaitUtterance()`. `StopPlayback()` drains queue + aborts current. Feed render frames to pipeline for AEC: each chunk fed to speaker ALSO goes through `pipeline.FeedRender()`. | `internal/tts/queue.go`, `internal/tts/queue_test.go` | `NewQueue(ttsClient, speaker, pipeline)` → `Start()` → `Enqueue(job)` → worker processes → `StopPlayback()` | `go test -race ./internal/tts/...` |
| 11 | **Unix socket server.** Listen on `/tmp/voice-daemon.sock`. Protocol compatible with existing Hammerspoon `init.lua`. Commands: `start` → `"started\n"` (begin recording), `stop` → `"<transcript>\n"` (flush + return all text), `cancel` → `"cancelled\n"`, `status` → `"idle\|listening\|recording\|processing\n"`. Push: `transcript:<text>\n` sent when utterance transcribed. Keep connection alive during `start` session for transcript pushes. Clean up stale socket on startup. `chmod 0666` socket. | `internal/daemon/socket.go`, `internal/daemon/socket_test.go` | `NewSocketServer(cfg, pipeline, sttClient)` → `Start(ctx)` → accept → handle command → push transcripts | `go test -race ./internal/daemon/...` (test with net.Dial to unix socket, send "status", expect "idle") |
| 12 | **HTTP server.** Listen on `:5111`. Three endpoints: `POST /speak` (JSON: `{text, backend?, model?, voice?}` → enqueue TTS job → `{status: "queued", queue_depth, backend}`), `POST /stop` (drain TTS queue → `{status: "stopped"}`), `GET /health` (return `{status, queue_depth, speaches_url, pocket_tts_url, stt_url, stt_socket}`). Use `net/http` + `encoding/json` (no framework — keep deps minimal). Graceful shutdown with context timeout. | `internal/daemon/http.go`, `internal/daemon/http_test.go` | `NewHTTPServer(cfg, ttsQueue)` → `Start(ctx)` → `POST /speak` → queue.Enqueue → JSON response | `go test -race ./internal/daemon/...` (test with httptest.Server) |
| 13 | **Daemon orchestrator.** `Daemon` struct owns: Pipeline, STT Client, TTS Client, TTS Queue, Speaker, Socket Server, HTTP Server. `Run(ctx)` starts everything in order: speaker.Open → pipeline.Start(onUtterance) → socket server → HTTP server. `onUtterance` callback: pipeline delivers audio → STT client.Transcribe → push transcript to socket server + accumulate for stop command. Graceful shutdown on context cancel: HTTP shutdown(5s) → socket close → pipeline.Stop → speaker.Close. Signal handling (SIGINT, SIGTERM) cancels context. | `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go` | `NewDaemon(cfg)` → `Run(ctx)` → starts all subsystems → blocks until ctx done → graceful shutdown | `go build ./cmd/voicedaemon/ && echo "daemon builds"` |
| 14 | **CLI entry point.** Kong CLI with flags: `--port` (default 5111), `--socket-path` (default /tmp/voice-daemon.sock), `--speaches-url`, `--pocket-tts-url`, `--stt-url`, `--stt-model`, `--stt-language`, `--speaches-model`, `--speaches-voice`, `--no-apm` (build tag override at runtime — if noapm build, warn and continue; if apm build but flag set, use no-op processor), `--debug` (verbose logging). Env var support: all flags also settable via env (e.g., `SPEACHES_URL`). Constructs config, creates Daemon, calls `daemon.Run(ctx)`. | `cmd/voicedaemon/main.go` | `main()` → Kong parse → build config → `NewDaemon(cfg)` → `daemon.Run(ctx)` | `go build ./cmd/voicedaemon/ && ./voicedaemon --help` shows all flags |
| 15 | **README + documentation.** Architecture diagram (ASCII). Installation instructions (Go install + system deps). Configuration table (all flags + env vars). Socket protocol reference. HTTP API reference. Sample rate topology diagram. Build instructions (with and without APM). | `README.md` | N/A — documentation | Human review |
| 16 | **Integration smoke test.** Create `scripts/smoke-test.sh` that: (1) builds binary, (2) starts daemon with `--no-apm` in background, (3) sends `status` to unix socket → expects `idle`, (4) `curl localhost:5111/health` → expects JSON with status=ok, (5) kills daemon, (6) verifies clean shutdown (no stale socket). Does NOT require Speaches running — tests infrastructure only. | `scripts/smoke-test.sh` | N/A — script | `bash scripts/smoke-test.sh` passes |

---

## go-webrtc-apm API Reference

```go
import apm "github.com/jfreymuth/go-webrtc-apm"

// Create processor
processor, err := apm.New(apm.Config{
    NumChannels:      1,
    NoiseSuppression: &apm.NoiseSuppressionConfig{Enabled: true, Level: apm.NoiseSuppressionHigh},
    VoiceDetection:   &apm.VoiceDetectionConfig{Enabled: true, Likelihood: apm.VoiceDetectionModerate},
    EchoCancellation: &apm.EchoCancellationConfig{Enabled: true, SuppressionLevel: apm.SuppressionHigh, EnableDelayAgnostic: true},
    AutoGainControl:  &apm.AutoGainControlConfig{Enabled: true, Mode: apm.AdaptiveDigital},
    HighPassFilter:   true,
})
defer processor.Close()

// Per-frame: 10ms at 48kHz = 480 samples float32
cleanSamples, hasVoice, err := processor.ProcessCapture(micFrame)
err = processor.ProcessRender(speakerFrame)  // feed speaker audio for AEC
stats := processor.GetStats()
```

**Constraints:** Fixed 48kHz. 10ms frames (480 samples). Thread-safe. ~60µs per frame.
**System dep:** `libwebrtc-audio-processing-dev` (apt) or build from source (macOS).

---

## Speaches API Reference

**STT:**
```
POST /v1/audio/transcriptions
Content-Type: multipart/form-data
  file: audio.wav (16kHz mono s16le)
  model: deepdml/faster-whisper-large-v3-turbo-ct2
  language: en
→ {"text": "transcribed words"}
```

**TTS:**
```
POST /v1/audio/speech
Content-Type: application/json
  {"model": "speaches-ai/Kokoro-82M-v1.0-ONNX", "input": "text", "voice": "af_heart", "response_format": "pcm"}
→ streaming PCM s16le bytes (sample rate depends on model)
```

**PocketTTS** uses identical `/v1/audio/speech` API shape. Requires `"stream": true` in body. Always 24000 Hz output.

---

## Unix Socket Protocol (Hammerspoon Compatible)

```
Client → Server:
  "start\n"    → Server replies "started\n", begins recording.
                  Connection stays open. Server pushes "transcript:<text>\n" on each utterance.
  "stop\n"     → Server replies "<all accumulated text>\n", stops recording.
  "cancel\n"   → Server replies "cancelled\n", discards everything.
  "status\n"   → Server replies "idle\n" | "listening\n" | "recording\n" | "processing\n"

Server → Client (push, only during active session):
  "transcript:<text>\n"  → Sent each time silence is detected and transcription completes.
```

---

## Sample Rate Topology

```
portaudio mic capture        → 48000 Hz (go-webrtc-apm requirement)
go-webrtc-apm processing     → 48000 Hz (fixed, non-negotiable)
Speaches STT input           → 16000 Hz (Whisper expects 16kHz WAV)
Speaches TTS output (Kokoro) → 24000 Hz
Speaches TTS output (Piper)  → 22050 Hz
PocketTTS output             → 24000 Hz
portaudio speaker playback   → 48000 Hz (match capture for AEC symmetry)
```

Resampling boundaries: 48k→16k before STT. TTS-rate→48k before ProcessRender + speaker output.

---

## Lessons from osog Audit (Apply These from Day One)

These patterns were learned the hard way. The osog audit found these exact bugs. Do not repeat them:

| osog Finding | Pattern to Follow Here |
|-------------|----------------------|
| C-2: use-after-free on APM close | Pipeline.Stop(): cancel ctx → WaitGroup on goroutine → THEN Close APM |
| C-3: data race on speaker buffer | Hold lock through buffer write + stream.Write. Never release between copy and Write |
| C-4: NaN in Kalman filter | Guard all divisions: check denominator > epsilon, check result for NaN/Inf |
| C-7: channel close race | Mutex-protected channel close with `closed` flag guard |
| H-1: discarded APM errors | Never `_ = processor.ProcessCapture(...)` — log all errors |
| H-6: panic in library code | No `mustX()` panic functions. Return errors, always |
| L-7: allocation in audio path | Use sync.Pool or pre-allocated buffers in hot paths (100Hz frame rate) |
| General | Use `logf func(string, ...any)` callback pattern, not `log.Printf` |

---

## Configuration Defaults

Match the Python daemon's defaults for drop-in replacement:

| Setting | Default | Env Var |
|---------|---------|---------|
| HTTP port | 5111 | `DAEMON_PORT` |
| Socket path | /tmp/voice-daemon.sock | `STT_SOCKET_PATH` |
| Speaches URL | http://localhost:34331 | `SPEACHES_URL` |
| PocketTTS URL | http://localhost:49112 | `POCKET_TTS_URL` |
| STT URL | http://localhost:34331 | `STT_URL` |
| STT model | deepdml/faster-whisper-large-v3-turbo-ct2 | `STT_MODEL` |
| STT language | en | `STT_LANGUAGE` |
| Speaches TTS model | speaches-ai/Kokoro-82M-v1.0-ONNX | `SPEACHES_MODEL` |
| Speaches TTS voice | af_heart | `SPEACHES_VOICE` |
| PocketTTS voice | alba | `POCKET_TTS_VOICE` |
| VAD pre-buffer | 800ms (25 chunks at 32ms) | — |
| VAD silence gap | 640ms (20 chunks) | — |
| Output sample rate | 48000 Hz | — |
| APM sample rate | 48000 Hz | — |

---

## Quality Gates

**Note:** `libwebrtc-audio-processing` is not installed on the build machine. Write `apm.go` for correctness but all builds and tests use `-tags noapm`. Do not debug cgo linking.

```bash
go build -tags noapm ./...
go test -race -tags noapm ./...
golangci-lint run
bash scripts/smoke-test.sh
```

---

## Acceptance (Literal)

Sprint is complete when:

- [ ] `go build -tags noapm ./cmd/voicedaemon/` produces a binary
- [ ] `go test -race -tags noapm ./...` passes with zero failures
- [ ] `golangci-lint run` returns zero issues (with errcheck enabled)
- [ ] `./voicedaemon --help` shows all flags with defaults matching table above
- [ ] `bash scripts/smoke-test.sh` passes (socket status + HTTP health, no Speaches required)
- [ ] Unix socket accepts `status` command and returns `idle`
- [ ] `curl localhost:5111/health` returns JSON with `status: ok`
- [ ] `curl -X POST localhost:5111/speak -d '{"text":"hello"}'` returns `{status: "queued"}`
- [ ] Daemon shuts down cleanly on SIGINT (no stale socket, no goroutine leaks)
- [ ] No `log.Printf` anywhere — all logging uses `logf` callback or `slog`
- [ ] No `_ = ` discarding errors from APM, HTTP, or portaudio operations
- [ ] README documents all flags, env vars, socket protocol, and HTTP API

Sprint is NOT complete if:

- Binary requires Speaches running to start (it should start and wait for commands)
- Any audio path hardcodes a sample rate instead of using constants
- Pipeline.Stop() closes APM before capture goroutine exits
- Speaker.Feed() releases lock between buffer copy and stream write
- Any parenthetical caveats in the completion report

---

## Completion Protocol

1. Write report to `./docs/reports/TASK-sprint-VD-1.md`
2. List each task and what was done
3. Paste quality gate output
4. Signal completion:

```bash
SPRINT="VD-1" && say -v Daniel "Voice daemon sprint $SPRINT complete" && osascript -e "display dialog \"Voice Daemon Sprint $SPRINT Complete\" buttons {\"OK\"} default button \"OK\" with title \"🔔 Claude Code\""
```
