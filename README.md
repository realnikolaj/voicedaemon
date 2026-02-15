# voicedaemon

Standalone STT+TTS daemon in Go. Replaces a Python voice daemon with a single static binary featuring portaudio capture, WebRTC audio processing (noise suppression, AGC, VAD, echo cancellation), Speaches STT, and dual TTS backends.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     voicedaemon                          │
│                                                          │
│  ┌──────────┐    ┌──────────┐    ┌──────────────────┐   │
│  │   Mic    │───▶│   APM    │───▶│  VAD State       │   │
│  │ (48kHz)  │    │ (NS/AGC/ │    │  Machine         │   │
│  │PortAudio │    │  VAD/AEC)│    │ IDLE→LISTEN→     │   │
│  └──────────┘    └────┬─────┘    │ RECORD→PROCESS   │   │
│                       │          └────────┬─────────┘   │
│                       │                   │              │
│  ┌──────────┐    ┌────┴─────┐    ┌────────▼─────────┐   │
│  │ Speaker  │◀───│  AEC     │    │  STT Client      │   │
│  │ (48kHz)  │    │ Render   │    │  (Speaches)      │   │
│  │PortAudio │    │ Path     │    │  48k→16k→WAV     │   │
│  └────▲─────┘    └──────────┘    └────────┬─────────┘   │
│       │                                   │              │
│  ┌────┴─────────────────┐        ┌────────▼─────────┐   │
│  │  TTS Queue           │        │  Unix Socket     │   │
│  │  (latest-wins drain) │        │  Server          │   │
│  │  Speaches + PocketTTS│        │  (/tmp/voice-    │   │
│  └──────────────────────┘        │   daemon.sock)   │   │
│                                  └──────────────────┘   │
│  ┌──────────────────────┐                               │
│  │  HTTP Server (:5111) │                               │
│  │  /speak /stop /health│                               │
│  └──────────────────────┘                               │
└─────────────────────────────────────────────────────────┘
```

## Installation

### System Dependencies

```bash
# macOS
brew install portaudio

# Required for real APM (noise suppression, AGC, echo cancellation):
brew install webrtc-audio-processing

# Verify webrtc-audio-processing is available:
pkg-config --cflags --libs webrtc-audio-processing-2
```

### Build

```bash
# Default build — real WebRTC APM (requires webrtc-audio-processing-2):
make build

# Fallback build — no-op stub with energy-based VAD (no webrtc dependency):
make build-noapm
```

### Run

```bash
./voicedaemon
# or with options:
./voicedaemon --port 5111 --debug
```

## APM Build Modes

voicedaemon supports two build modes:

| Mode | Build Tag | APM | VAD | AEC | Dependency |
|------|-----------|-----|-----|-----|------------|
| **Real** (default) | *(none)* | WebRTC NS+AGC2+HPF | WebRTC voice detection | AEC3 | `webrtc-audio-processing-2` |
| **Stub** | `noapm` | pass-through | RMS energy threshold | none | *(none)* |

The real APM wraps `libwebrtc-audio-processing-2` via a C++ shim (`internal/capm/`).
The C++ wrapper (`apm_wrapper.h`, `apm_wrapper.cpp`) provides a flat C API around the
WebRTC `AudioProcessing` module, adapted from the [osog](https://github.com/realnikolaj/osog) project.

## Configuration

All flags can be set via CLI flags or environment variables.

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--port` | `DAEMON_PORT` | `5111` | HTTP server port |
| `--socket-path` | `STT_SOCKET_PATH` | `/tmp/voice-daemon.sock` | Unix socket path |
| `--speaches-url` | `SPEACHES_URL` | `http://localhost:34331` | Speaches server URL |
| `--pocket-tts-url` | `POCKET_TTS_URL` | `http://localhost:49112` | PocketTTS server URL |
| `--stt-url` | `STT_URL` | `http://localhost:34331` | STT server URL |
| `--stt-model` | `STT_MODEL` | `deepdml/faster-whisper-large-v3-turbo-ct2` | STT model |
| `--stt-language` | `STT_LANGUAGE` | `en` | STT language |
| `--speaches-model` | `SPEACHES_MODEL` | `speaches-ai/Kokoro-82M-v1.0-ONNX` | Speaches TTS model |
| `--speaches-voice` | `SPEACHES_VOICE` | `af_heart` | Speaches TTS voice |
| `--pocket-voice` | `POCKET_TTS_VOICE` | `alba` | PocketTTS voice |
| `--debug` | `VOICEDAEMON_DEBUG` | `false` | Enable debug logging |

## Sample Rate Topology

```
portaudio mic capture        → 48000 Hz
APM processing               → 48000 Hz
Speaches STT input           → 16000 Hz (resampled from 48kHz)
Speaches TTS output (Kokoro) → 24000 Hz (resampled to 48kHz)
Speaches TTS output (Piper)  → 22050 Hz (resampled to 48kHz)
PocketTTS output             → 24000 Hz (resampled to 48kHz)
portaudio speaker playback   → 48000 Hz
```

## Unix Socket Protocol

The daemon listens on a Unix socket compatible with Hammerspoon integration.

```
Client → Server:
  "start\n"    → "started\n"     Begin recording. Connection stays open.
  "stop\n"     → "<transcript>\n" Stop recording, return accumulated text.
  "cancel\n"   → "cancelled\n"   Discard recording.
  "status\n"   → "idle\n" | "listening\n" | "recording\n" | "processing\n"

Server → Client (push during active session):
  "transcript:<text>\n"          Sent on each utterance completion.
```

## HTTP API

### POST /speak

Enqueue text for TTS playback.

```bash
curl -X POST localhost:5111/speak \
  -H 'Content-Type: application/json' \
  -d '{"text": "Hello world"}'
# → {"status":"queued","queue_depth":1,"backend":"speaches"}

# With backend selection:
curl -X POST localhost:5111/speak \
  -d '{"text": "Hello", "backend": "pocket"}'

# With model/voice override:
curl -X POST localhost:5111/speak \
  -d '{"text": "Hello", "model": "custom-model", "voice": "alice"}'
```

### POST /stop

Stop current TTS playback and drain queue.

```bash
curl -X POST localhost:5111/stop
# → {"status":"stopped"}
```

### GET /health

Health check with configuration info.

```bash
curl localhost:5111/health
# → {"status":"ok","queue_depth":0,"speaches_url":"...","pocket_tts_url":"...","stt_url":"...","stt_socket":"..."}
```

## Build Commands

```bash
make build        # Build binary (real APM)
make build-noapm  # Build binary (stub, no webrtc dependency)
make test         # Run tests with race detector (real APM)
make test-noapm   # Run tests with race detector (stub)
make lint         # Run golangci-lint (real APM)
make lint-noapm   # Run golangci-lint (stub)
make check        # All of the above (real APM)
make check-noapm  # All of the above (stub)
make smoke        # Build stub + smoke test
```

## Attribution

The WebRTC C++ wrapper (`internal/capm/apm_wrapper.h`, `internal/capm/apm_wrapper.cpp`)
is adapted from the [osog](https://github.com/realnikolaj/osog) project.
