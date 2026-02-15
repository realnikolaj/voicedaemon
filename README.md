# voicedaemon

**v0.2.7** — Standalone STT+TTS daemon in Go. Single binary, portaudio I/O, dual TTS backends, Unix socket STT, HTTP API.

## Build Modes

| Mode | Build Tag | Output Rate | APM | AEC | Dependency |
|------|-----------|-------------|-----|-----|------------|
| **noapm** (recommended) | `-tags noapm` | 24kHz (native Kokoro) | energy VAD | none | portaudio only |
| **Real APM** | *(none)* | 48kHz (AEC symmetry) | WebRTC NS+AGC2+VAD | AEC3 | `webrtc-audio-processing-2` |

## Interfaces

| Interface | Protocol | Purpose |
|-----------|----------|---------|
| HTTP `:5111` | REST | TTS playback (`/speak`, `/stop`, `/health`) |
| Unix socket | Line protocol | STT recording (`start`, `stop`, `cancel`, `status`) |

## TTS Backends

| Backend | Engine | Voices | Sample Rate |
|---------|--------|--------|-------------|
| **Speaches** (Kokoro) | `speaches-ai/Kokoro-82M-v1.0-ONNX` | `af_heart`, `af_bella`, `am_adam` | 24000 Hz |
| **Speaches** (Piper/GLaDOS) | `piper`, `glados` | model-specific | 22050 Hz |
| **PocketTTS** | `piper` | `alba`, `lessac` | 24000 Hz |

## Prerequisites

```bash
# Go 1.25+
go version

# portaudio (required)
brew install portaudio

# webrtc-audio-processing (only for real APM build)
brew install webrtc-audio-processing
pkg-config --cflags --libs webrtc-audio-processing-2
```

## Install

```bash
go install -tags noapm github.com/realnikolaj/voicedaemon/cmd/voicedaemon@latest
```

## Usage

```bash
# Start with defaults (Speaches on localhost:34331)
voicedaemon --debug

# Custom Speaches URL
voicedaemon --speaches-url http://192.168.1.10:34331 --debug

# Version
voicedaemon --version
```

### TTS via HTTP

```bash
# Default voice (Kokoro af_heart)
curl -X POST localhost:5111/speak \
  -d '{"text": "Hello world"}'

# Different voice
curl -X POST localhost:5111/speak \
  -d '{"text": "Hello", "voice": "af_bella"}'

# PocketTTS backend
curl -X POST localhost:5111/speak \
  -d '{"text": "Hello", "backend": "pocket"}'

# Stop playback
curl -X POST localhost:5111/stop

# Health check
curl localhost:5111/health
```

### STT via Unix Socket

```bash
# Check status
echo "status" | socat - UNIX-CONNECT:/tmp/voice-daemon.sock

# Record and transcribe
echo "start" | socat - UNIX-CONNECT:/tmp/voice-daemon.sock
# (speak into mic)
echo "stop" | socat - UNIX-CONNECT:/tmp/voice-daemon.sock
# → returns transcript
```

## Performance

Measured on M1 Mac with Speaches (Kokoro) local:

| Stage | Median |
|-------|--------|
| STT transcription | ~130ms |
| TTS generation | ~400ms |
| End-to-end (speak → audio out) | ~775ms |

## Configuration

All flags have env var equivalents:

| Flag | Env | Default |
|------|-----|---------|
| `--port` | `DAEMON_PORT` | `5111` |
| `--socket-path` | `STT_SOCKET_PATH` | `/tmp/voice-daemon.sock` |
| `--speaches-url` | `SPEACHES_URL` | `http://localhost:34331` |
| `--pocket-tts-url` | `POCKET_TTS_URL` | `http://localhost:49112` |
| `--stt-url` | `STT_URL` | `http://localhost:34331` |
| `--stt-model` | `STT_MODEL` | `deepdml/faster-whisper-large-v3-turbo-ct2` |
| `--stt-language` | `STT_LANGUAGE` | `en` |
| `--speaches-model` | `SPEACHES_MODEL` | `speaches-ai/Kokoro-82M-v1.0-ONNX` |
| `--speaches-voice` | `SPEACHES_VOICE` | `af_heart` |
| `--pocket-voice` | `POCKET_TTS_VOICE` | `alba` |
| `--debug` | `VOICEDAEMON_DEBUG` | `false` |

## Integration

- **Hammerspoon**: Socket protocol for macOS hotkey → STT recording
- **Claude MCP**: `voice-mcp.py` bridges Claude Desktop to voicedaemon TTS/STT
- **osog**: Planned — full desktop voice assistant using voicedaemon as audio backend

## Quality Gates

```bash
make check        # build + test -race + golangci-lint (real APM)
make check-noapm  # build + test -race + golangci-lint (noapm)
```

## Known Issues

- Real APM VAD is deprecated upstream; noapm energy VAD is the working path
- Single STT session at a time (socket holds state)
- No echo cancellation in noapm mode — use headphones or mute during TTS

## Attribution

WebRTC C++ wrapper (`internal/capm/`) adapted from [osog](https://github.com/realnikolaj/osog).
