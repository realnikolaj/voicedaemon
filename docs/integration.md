# voicedaemon Integration Guide

## Quick Start (Go)

```bash
go get github.com/realnikolaj/voicedaemon/pkg/vdclient
```

### TTS via HTTP

```go
import "github.com/realnikolaj/voicedaemon/pkg/vdclient"

client := vdclient.New("http://localhost:5111")

// Check connectivity
health, err := client.Health(ctx)
if err != nil {
    log.Fatal("daemon not running:", err)
}

// Speak — returns when queued, not when playback finishes
resp, err := client.Speak(ctx, vdclient.SpeakRequest{
    Text:  "Hello world",
    Voice: "af_heart",
})

// Stop playback
err = client.Stop(ctx)
```

### STT via Unix Socket

```go
session, err := vdclient.Dial("/tmp/voice-daemon.sock")
defer session.Close()

// Option A: accumulate, then use
session.Start()
// ... user speaks ...
transcript, err := session.Stop()
fmt.Println(transcript)

// Option B: stream transcripts as they arrive
session.Start()
go func() {
    for text := range session.Transcripts() {
        fmt.Println("heard:", text)
    }
}()
// ... later ...
full, err := session.Stop()
```

## Quick Start (Any Language)

### HTTP API

```bash
# Health check — call on startup
curl localhost:5111/health
# → {"status":"ok","queue_depth":0,"speaches_url":"...","pocket_tts_url":"...","stt_url":"...","stt_socket":"..."}

# TTS playback
curl -X POST localhost:5111/speak \
  -H 'Content-Type: application/json' \
  -d '{"text":"Hello","voice":"af_heart"}'
# → {"status":"queued","queue_depth":1,"backend":"speaches"}

# With backend selection
curl -X POST localhost:5111/speak \
  -d '{"text":"Hello","backend":"pocket","voice":"alba"}'

# Stop playback
curl -X POST localhost:5111/stop
# → {"status":"stopped"}
```

### Unix Socket Protocol

Line-based text protocol over Unix domain socket at `/tmp/voice-daemon.sock`.

```
Client → Server:
  "start\n"    → "started\n"       Begin recording
  "stop\n"     → "<transcript>\n"  Stop, return accumulated text
  "cancel\n"   → "cancelled\n"     Discard recording
  "status\n"   → "idle\n" | "listening\n" | "recording\n" | "processing\n"

Server → Client (push during active session):
  "transcript:<text>\n"            Sent on each utterance completion
```

Example with socat:

```bash
echo "status" | socat - UNIX-CONNECT:/tmp/voice-daemon.sock
# → idle
```

## API Reference

### POST /speak

Queue text for TTS playback.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `text` | string | yes | Text to speak |
| `backend` | string | no | `"speaches"` (default) or `"pocket"` |
| `model` | string | no | TTS model override |
| `voice` | string | no | Voice ID (e.g. `"af_heart"`, `"alba"`) |

**Response** `200 OK`:
```json
{"status": "queued", "queue_depth": 1, "backend": "speaches"}
```

**Error** `400 Bad Request`:
```json
{"error": "text is required"}
```

### POST /stop

Stop current playback and clear queue.

**Response** `200 OK`:
```json
{"status": "stopped"}
```

### GET /health

Check daemon connectivity.

**Response** `200 OK`:
```json
{
  "status": "ok",
  "queue_depth": 0,
  "speaches_url": "http://localhost:34331",
  "pocket_tts_url": "http://localhost:49112",
  "stt_url": "http://localhost:34331",
  "stt_socket": "/tmp/voice-daemon.sock"
}
```

## Architecture Notes for Consumers

- **voicedaemon must be running** before your app connects. Use `Health()` on startup to fail fast.
- **One STT session at a time.** The socket holds state for a single recording session.
- **TTS queue is FIFO.** Multiple `Speak()` calls queue sequentially; they don't interrupt each other.
- **`Speak()` returns when queued**, not when playback finishes. Use this for fire-and-forget TTS.
- **Backend switching is per-request.** Pass `"speaches"` or `"pocket"` in each `SpeakRequest`.
- **Voice selection is per-request.** Pass the voice ID directly; no global config needed.

## Common Patterns

### Check then use

```go
client := vdclient.New("http://localhost:5111")
if _, err := client.Health(ctx); err != nil {
    log.Fatal("voicedaemon not running — start it first")
}
// safe to use
```

### Fire and forget TTS

```go
// Returns immediately after queueing
client.Speak(ctx, vdclient.SpeakRequest{Text: "Processing your request..."})
// Continue with other work while audio plays
```

### Stream transcripts

```go
session.Start()
go func() {
    for text := range session.Transcripts() {
        // Process each utterance as it arrives
        handleUtterance(text)
    }
}()
```

### Accumulate then use

```go
session.Start()
// Ignore the Transcripts() channel entirely
transcript, _ := session.Stop()
// Use the full joined transcript
```

## Integration Examples

### Hammerspoon

The socket protocol is designed for Hammerspoon hotkey integration. Bind a key to toggle recording:

```lua
-- Connect, send "start", read "started"
-- On key release: send "stop", read transcript, paste into focused app
```

### Claude MCP

`voice-mcp.py` bridges Claude Desktop to voicedaemon:

```python
# MCP tool: speak(text) → POST /speak
# MCP tool: listen() → socket start/stop → return transcript
```
