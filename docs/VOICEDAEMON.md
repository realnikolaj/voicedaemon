# VOICEDAEMON.md — Overseer Context Artifact

## 1. Identity

| Field | Value |
|-------|-------|
| Name | voicedaemon |
| Repo | `github.com/realnikolaj/voicedaemon` |
| Language | Go 1.25+ (cgo optional for WebRTC APM) |
| Version | 0.2.7 |
| License | MIT |
| Entry point | `cmd/voicedaemon/main.go` |
| Module path | `github.com/realnikolaj/voicedaemon` |

## 2. What It Is

Standalone STT+TTS daemon in Go. Single binary, captures audio via portaudio, detects voice with VAD (energy-based or WebRTC APM), transcribes via Speaches STT, and plays synthesized speech from dual TTS backends (Speaches/Kokoro, PocketTTS). Exposes an HTTP API for TTS control and a Unix socket for STT recording sessions. Consumed by Hammerspoon hotkeys, Claude MCP bridges (`voice-mcp.py`), and planned as the audio backend for the **osog** desktop voice assistant. Ships a zero-dependency Go client package at `pkg/vdclient/` for programmatic integration.

## 3. Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        voicedaemon binary                       │
│                                                                 │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌─────────────┐  │
│  │ portaudio │──▶│   APM    │──▶│   VAD    │──▶│ STT Client  │  │
│  │ mic 48kHz │   │ NS+AGC+  │   │ state    │   │ POST /v1/   │──┼──▶ Speaches :34331
│  │           │   │ AEC(opt) │   │ machine  │   │ audio/trans  │  │    (Whisper)
│  └──────────┘   └──────────┘   └──────────┘   └─────────────┘  │
│                                                                 │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐                    │
│  │ portaudio │◀──│ Speaker  │◀──│TTS Queue │◀─── HTTP :5111     │
│  │ out 24/48k│   │ prebuf+  │   │  FIFO    │    POST /speak    │
│  │           │   │ resample │   │          │    POST /stop     │
│  └──────────┘   └──────────┘   └──────────┘    GET  /health   │
│                       │                                         │
│                       ▼                                         │
│              ┌──────────────┐                                   │
│              │  TTS Client  │──▶ Speaches :34331 (Kokoro/Piper) │
│              │  (streaming) │──▶ PocketTTS :49112               │
│              └──────────────┘                                   │
│                                                                 │
│  ┌──────────────────┐                                           │
│  │ Unix Socket      │◀──▶ Hammerspoon / socat / vdclient       │
│  │ /tmp/voice-      │     Line protocol: start/stop/cancel/     │
│  │  daemon.sock     │     status + transcript:<text> push       │
│  └──────────────────┘                                           │
└─────────────────────────────────────────────────────────────────┘

Data formats:
  Mic capture  → float32 48kHz mono
  APM frames   → 480 samples = 10ms @ 48kHz
  STT input    → 16-bit PCM WAV @ 16kHz (resampled from 48k)
  TTS output   → raw PCM s16le @ 24kHz (Kokoro) or 22050 Hz (Piper)
  Speaker out  → int16 @ 24kHz (noapm) or 48kHz (real APM)
  Socket proto → line-based text over Unix domain socket
  HTTP API     → JSON over TCP
```

## 4. Package/Module Map

```
voicedaemon/
├── cmd/voicedaemon/main.go      # Kong CLI, signal handling, logf factory
├── internal/
│   ├── audio/
│   │   ├── audio.go             # Sample rate constants (48k, 16k, 24k, 22050)
│   │   ├── apm.go               # Real WebRTC APM via internal/capm (!noapm)
│   │   ├── apm_noapm.go         # Energy-VAD stub (noapm build tag)
│   │   ├── rate.go              # OutputSampleRate=48000 (!noapm)
│   │   ├── rate_noapm.go        # OutputSampleRate=24000 (noapm)
│   │   ├── mic.go               # portaudio input stream, frame channel
│   │   ├── speaker.go           # portaudio output, prebuffer gate, FIFO queue
│   │   ├── vad.go               # VAD state machine (idle→listen→record→process)
│   │   ├── pipeline.go          # Orchestrates mic→APM→VAD, AEC render feed
│   │   ├── resample.go          # Linear interpolation resampler (float32 + s16le)
│   │   └── *_test.go            # Table-driven tests for resample, VAD, APM
│   ├── capm/
│   │   ├── capm.go              # cgo bindings to WebRTC APM C++ wrapper
│   │   ├── apm_wrapper.h        # C header for APM functions
│   │   └── apm_wrapper.cpp      # C++ impl: NS, AGC2, HPF, AEC3
│   ├── stt/
│   │   ├── client.go            # Speaches STT HTTP client, WAV encoding
│   │   └── client_test.go       # httptest-based STT tests
│   ├── tts/
│   │   ├── client.go            # Dual-backend TTS streaming (Speaches+Pocket)
│   │   ├── client_test.go       # Stream tests with httptest
│   │   ├── queue.go             # FIFO job queue, AEC render feed, abort
│   │   └── queue_test.go        # Queue serialization + cancellation tests
│   └── daemon/
│       ├── daemon.go            # Lifecycle orchestrator, wires all subsystems
│       ├── http.go              # /speak, /stop, /health endpoints
│       ├── http_test.go         # HTTP routing + backend selection tests
│       ├── socket.go            # Unix socket server, line protocol
│       └── socket_test.go       # Socket protocol + transcript push tests
├── pkg/vdclient/
│   ├── client.go                # HTTP client: Speak(), Stop(), Health()
│   ├── socket.go                # Unix socket client: Dial(), Start/Stop, Transcripts()
│   ├── types.go                 # SpeakRequest, SpeakResponse, HealthResponse
│   ├── client_test.go           # 6 HTTP client tests
│   ├── socket_test.go           # 5 socket protocol tests
│   └── example_test.go          # Runnable examples
├── reference/                   # osog APM source (build-tagged ignore)
├── scripts/smoke-test.sh        # 6-step integration smoke test
├── docs/
│   ├── integration.md           # Consumer guide (Go + any-language)
│   └── reports/TASK-*.md        # Sprint completion reports
├── CLAUDE.md                    # AI coding assistant instructions
├── Makefile                     # Dual-build quality gates
└── go.mod                       # 2 deps: kong, portaudio
```

## 5. Public API / CLI

### CLI Flags

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--port` | `DAEMON_PORT` | `5111` | HTTP server port |
| `--socket-path` | `STT_SOCKET_PATH` | `/tmp/voice-daemon.sock` | Unix socket path |
| `--speaches-url` | `SPEACHES_URL` | `http://localhost:34331` | Speaches server |
| `--pocket-tts-url` | `POCKET_TTS_URL` | `http://localhost:49112` | PocketTTS server |
| `--stt-url` | `STT_URL` | `http://localhost:34331` | STT server |
| `--stt-model` | `STT_MODEL` | `deepdml/faster-whisper-large-v3-turbo-ct2` | Whisper model |
| `--stt-language` | `STT_LANGUAGE` | `en` | STT language |
| `--speaches-model` | `SPEACHES_MODEL` | `speaches-ai/Kokoro-82M-v1.0-ONNX` | TTS model |
| `--speaches-voice` | `SPEACHES_VOICE` | `af_heart` | TTS voice |
| `--pocket-voice` | `POCKET_TTS_VOICE` | `alba` | PocketTTS voice |
| `--debug` | `VOICEDAEMON_DEBUG` | `false` | Debug logging |
| `--version` | — | — | Print version |

### HTTP API (`:5111`)

| Method | Path | Request Body | Response | Description |
|--------|------|-------------|----------|-------------|
| `POST` | `/speak` | `{"text":"...","backend?":"speaches\|pocket","model?":"...","voice?":"..."}` | `{"status":"queued","queue_depth":N,"backend":"..."}` | Queue TTS |
| `POST` | `/stop` | — | `{"status":"stopped"}` | Cancel playback + drain queue |
| `GET` | `/health` | — | `{"status":"ok","queue_depth":N,"speaches_url":"...","pocket_tts_url":"...","stt_url":"...","stt_socket":"..."}` | Health check |

### Unix Socket Protocol (`/tmp/voice-daemon.sock`)

```
Client → Server:
  start\n    → started\n             Begin recording
  stop\n     → <transcript>\n        Stop + return text
  cancel\n   → cancelled\n           Discard recording
  status\n   → idle|listening|recording|processing\n

Server → Client (push during active session):
  transcript:<text>\n                Per-utterance push
```

### Published Go Package: `pkg/vdclient/`

```go
import "github.com/realnikolaj/voicedaemon/pkg/vdclient"

// HTTP client
func New(baseURL string) *Client
func (c *Client) Speak(ctx, SpeakRequest) (*SpeakResponse, error)
func (c *Client) Stop(ctx) error
func (c *Client) Health(ctx) (*HealthResponse, error)

// Socket client
func Dial(socketPath string) (*STTSession, error)
func (s *STTSession) Start() error
func (s *STTSession) Stop() (string, error)
func (s *STTSession) Cancel() error
func (s *STTSession) Status() (string, error)
func (s *STTSession) Transcripts() <-chan string
func (s *STTSession) Close() error
```

Zero external dependencies — consumers don't inherit voicedaemon's dep tree.

## 6. Dependencies

### Runtime Services

| Service | Default Address | Required | Purpose |
|---------|----------------|----------|---------|
| Speaches (STT+TTS) | `http://localhost:34331` | Yes | Whisper STT + Kokoro/Piper TTS |
| PocketTTS | `http://localhost:49112` | No | Alternative TTS backend |
| portaudio | system library | Yes | Audio I/O |
| webrtc-audio-processing-2 | system library | Only for real APM build | NS, AGC, AEC3 |

### Go Module Dependencies

| Module | Version | Purpose |
|--------|---------|---------|
| `github.com/alecthomas/kong` | v1.14.0 | CLI flag parsing |
| `github.com/gordonklaus/portaudio` | v0.0.0-20260203 | portaudio Go bindings |

### Cross-Project Dependencies

| Project | Dependency Type | Details |
|---------|----------------|---------|
| **osog** | Source origin | `internal/capm/` C++ wrapper adapted from osog's Phase 4 go-webrtc-apm bindings |
| **osog** | Planned consumer | osog will `go get` `pkg/vdclient/` as audio backend |
| **claude-speaks** | Integration | `voice-mcp.py` bridges Claude Desktop to voicedaemon HTTP+socket APIs |

## 7. Quality Gates

```bash
# Real APM build (requires webrtc-audio-processing-2 installed)
make check          # go build + go test -race + golangci-lint

# Stub build (no webrtc dependency, recommended)
make check-noapm    # go build -tags noapm + go test -race -tags noapm + golangci-lint --build-tags noapm

# Smoke test (builds noapm, starts daemon, tests socket+HTTP, clean shutdown)
make smoke          # build-noapm + bash scripts/smoke-test.sh

# Individual targets
make build          # Real APM binary
make build-noapm    # Stub binary
make test           # Tests with race detector
make test-noapm     # Tests with race detector (stub)
make lint           # golangci-lint (errcheck, govet, staticcheck, unused, ineffassign)
make lint-noapm     # golangci-lint with noapm tag
```

## 8. Completed Work

| Sprint | Date | Summary |
|--------|------|---------|
| VD-1 | 2026-02-15 | Full skeleton: 16 tasks, portaudio I/O, VAD, STT client, TTS streaming, Unix socket, HTTP API, Kong CLI, smoke test |
| VD-2.0 | 2026-02-15 | Real WebRTC APM via cgo in `internal/capm/`, dual build tags, 11 APM subtests |
| VD-2.1 | 2026-02-15 | TTS hotfix: PCM byte alignment guard (carry-byte), AEC render resampling to 48kHz |
| VD-2.2 | 2026-02-16 | Pre-buffer gate: 200ms accumulation before playback, atomic.Bool in callback |
| VD-2.3 | 2026-02-16 | Build-tagged output rate: noapm→24kHz native Kokoro, real APM→48kHz for AEC |
| VD-2.4 | 2026-02-16 | Speaker queue 200→2000 frames (20s buffer), 500ms timeout on enqueue |
| VD-2.5 | 2026-02-16 | Fix premature done signal: moved exclusively to portaudio callback |
| VD-2.6 | 2026-02-16 | FIFO queue serialization: removed latest-wins abort from Enqueue() |
| VD-2.7 | 2026-02-16 | TTS logf wiring verification, `--version` flag via Kong |
| VD-2.8 | 2026-02-16 | README rewrite to v0.2.7 |
| VD-3.0 | 2026-02-17 | Public `pkg/vdclient/` package (HTTP+socket), 11 tests, `docs/integration.md` |

## 9. Current State

### What Works

- noapm build: compiles, passes all tests with race detector, lint clean
- Real APM build: compiles, passes all tests (11 APM subtests), lint clean
- HTTP API: /speak queues FIFO, /stop drains+aborts, /health reports status
- Unix socket: start/stop/cancel/status + transcript push during sessions
- TTS: dual backend streaming (Speaches Kokoro @ 24kHz, PocketTTS @ 24kHz, Piper @ 22050 Hz)
- STT: Whisper transcription via Speaches, peak normalize + 48→16kHz resample + WAV encode
- Speaker: pre-buffer gate (200ms), residual frame alignment, 20s queue capacity
- Smoke test: 6-step automated validation (build, start, socket, HTTP, speak, shutdown)
- Client package: zero-dep `pkg/vdclient/` with full HTTP + socket coverage

### What's Broken / Incomplete

- Real APM VAD is deprecated upstream (v2 `voice_detected` may be empty) — noapm energy VAD is the working path
- No echo cancellation in noapm mode — requires headphones or mute during TTS
- Single STT session at a time (socket holds state)

### Key Metrics

| Metric | Value |
|--------|-------|
| Go packages | 7 (audio, capm, stt, tts, daemon, vdclient, cmd) |
| Test files | 9 |
| Test functions | ~35+ |
| External Go deps | 2 (kong, portaudio) |
| Build modes | 2 (noapm stub, real APM) |
| Sprint reports | 11 |

### Performance (M1 Mac + local Speaches)

| Stage | Median |
|-------|--------|
| STT transcription | ~130ms |
| TTS generation | ~400ms |
| End-to-end (speak → audio out) | ~775ms |

## 10. Planned Work

| Sprint | Repo | Scope | Cross-Repo Dependency |
|--------|------|-------|-----------------------|
| VD-4.0 | voicedaemon | Persistent config file (TOML/YAML), default voice profiles | None |
| VD-5.0 | voicedaemon | Multi-session socket support (concurrent STT clients) | None |
| osog integration | osog | Import `pkg/vdclient/`, wire to voice command pipeline | **Cross-repo gate:** voicedaemon tagged ≥ v0.3.0 with stable `pkg/vdclient/` API |
| claude-speaks update | claude-speaks | Update `voice-mcp.py` to use voicedaemon HTTP API instead of direct Speaches calls | voicedaemon running on target host |

## 11. Cross-Repo Contracts

### Published Package: `pkg/vdclient/`

| Consumer | Import Path | API Surface |
|----------|-------------|-------------|
| osog (planned) | `github.com/realnikolaj/voicedaemon/pkg/vdclient` | `New()`, `Speak()`, `Stop()`, `Health()`, `Dial()`, `Start()`, `Stop()`, `Transcripts()` |

**Stability:** API is considered unstable until v0.3.0 tag. Breaking changes possible.

### Shared Protocols

| Protocol | Format | Consumers |
|----------|--------|-----------|
| HTTP TTS API | JSON over TCP `:5111` | claude-speaks (voice-mcp.py), osog (via vdclient), any HTTP client |
| Unix socket STT | Line-based text, `\n` delimited | Hammerspoon, osog (via vdclient), socat |
| TTS PCM format | Raw s16le, rate varies by model | Internal (speaker.go consumes from tts/client.go) |
| STT audio format | 16-bit PCM WAV @ 16kHz | Internal (stt/client.go → Speaches) |

### Source Adaptation

| From | To | What |
|------|----|------|
| osog `internal/audio/apm_wrapper.*` | voicedaemon `internal/capm/` | C++ WebRTC APM wrapper, adapted for voicedaemon's isolated cgo package structure |

## 12. Known Issues / Debt

| Priority | Issue | Impact |
|----------|-------|--------|
| **High** | Real APM VAD deprecated in webrtc-audio-processing v2 | `voice_detected` may return empty; noapm energy VAD is the workaround |
| **High** | No AEC in noapm mode | Feedback loops when not using headphones; Go AEC without webrtc lib is non-trivial |
| **Medium** | Single STT session limit | Socket state machine supports one client; concurrent sessions would need multiplexing |
| **Medium** | Linear interpolation resampler | Quality acceptable for voice but not music; could upgrade to sinc/polyphase |
| **Low** | `time.Sleep` in queue tests | Flaky timing; should use channels/signals for synchronization |
| **Low** | No TLS/auth on HTTP API | Localhost-only assumption; fine for single-machine use |
| **Low** | 200ms pre-buffer adds latency | Imperceptible for TTS; could tune to 100ms if needed |

## 13. Conventions

### Coding Standards

- **Logging:** `logf func(string, ...any)` callback everywhere. No `log.Printf`.
- **Errors:** `fmt.Errorf("context: %w", err)`. No `_ =` discards. No `panic()`.
- **Audio frames:** 480 samples = 10ms @ 48kHz. Use `FrameSize` constant.
- **Resampling:** All in `internal/audio/resample.go`. Don't scatter.
- **portaudio callbacks:** `select { case ch <- frame: default: }` — never block.
- **Shutdown:** context cancel → WaitGroup wait → resource cleanup.
- **Tests:** Table-driven, required for all test functions.
- **C++ isolation:** All cgo + C++ source in `internal/capm/`, never in `internal/audio/`.

### Build Tags

| Tag | Effect |
|-----|--------|
| (none) | Real APM via cgo + webrtc-audio-processing-2 |
| `noapm` | Energy-VAD stub, no cgo, portaudio output at 24kHz |

### Versioning

- Semantic: `MAJOR.MINOR.PATCH`
- Current: `0.2.7` (set in `cmd/voicedaemon/main.go`, overridable via `-ldflags`)
- No tagged releases yet — pre-v1.0

### Branching

- `main` — stable, quality gates pass
- `dev/vd-N` — sprint branches

### Sprint Reports

- Location: `docs/reports/TASK-VD-{sprint}.md`
- Format: Status, What Was Done, Files Modified/NOT Modified, Verification commands, Notes
- Announced via TTS: `report-say.txt` template
