# VD-3.0: Public Go Client Package + Integration Docs

## Context
voicedaemon is a standalone STT+TTS daemon. Other Go projects (starting with osog) will import
a client to talk to it. Right now every consumer would need to reimplement the socket protocol
and HTTP calls. Ship a ready-made client in `pkg/vdclient/` so consumers just `go get` and use.

`pkg/` not `internal/` — this package is the public API for other Go modules.

## Task 1: pkg/vdclient/ — Go Client Package

### pkg/vdclient/client.go — HTTP client (TTS + health)

```go
type Client struct { ... }

func New(baseURL string) *Client

// Speak queues text for TTS playback. Blocks until accepted by daemon (not until playback finishes).
func (c *Client) Speak(ctx context.Context, req SpeakRequest) (*SpeakResponse, error)

// Stop cancels current playback and clears the TTS queue.
func (c *Client) Stop(ctx context.Context) error

// Health checks daemon connectivity and returns status.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error)
```

### pkg/vdclient/socket.go — Unix socket client (STT sessions)

```go
type STTSession struct { ... }

// Dial connects to the voicedaemon Unix socket.
func Dial(socketPath string) (*STTSession, error)

// Start begins a recording session. Returns after "started" confirmation.
func (s *STTSession) Start() error

// Stop ends the session and returns the accumulated transcript.
func (s *STTSession) Stop() (string, error)

// Cancel discards the session.
func (s *STTSession) Cancel() error

// Status returns current daemon state: "idle", "listening", "recording", "processing".
func (s *STTSession) Status() (string, error)

// Transcripts returns a channel that receives pushed transcript fragments
// during a recording session. Channel is closed when Stop() or Cancel() is called.
func (s *STTSession) Transcripts() <-chan string

// Close closes the underlying socket connection.
func (s *STTSession) Close() error
```

The socket protocol is line-based text:
- Client sends: `start\n`, `stop\n`, `cancel\n`, `status\n`
- Server responds: `started\n`, `<transcript>\n`, `cancelled\n`, `idle\n`/etc
- Server pushes during session: `transcript:<text>\n`

The `Transcripts()` channel approach: Start() launches a goroutine that reads lines from
the socket. Lines starting with `transcript:` get sent on the channel. Stop/Cancel signal
the goroutine to exit and close the channel. Non-transcript lines (the stop response) are
captured separately.

### pkg/vdclient/types.go — shared types

```go
type SpeakRequest struct {
    Text    string `json:"text"`
    Backend string `json:"backend,omitempty"` // "speaches" or "pockettts"
    Model   string `json:"model,omitempty"`
    Voice   string `json:"voice,omitempty"`
}

type SpeakResponse struct {
    Status    string `json:"status"`
    QueueDepth int   `json:"queue_depth"`
    Backend   string `json:"backend"`
}

type HealthResponse struct {
    Status       string `json:"status"`
    QueueDepth   int    `json:"queue_depth"`
    SpeachesURL  string `json:"speaches_url"`
    PocketTTSURL string `json:"pocket_tts_url"`
    STTURL       string `json:"stt_url"`
    STTSocket    string `json:"stt_socket"`
}
```

### pkg/vdclient/client_test.go — HTTP client tests

Test with httptest.NewServer mocking the daemon endpoints:
- TestSpeak — queues text, verifies request body, checks response parsing
- TestSpeakWithVoice — verifies backend/model/voice fields pass through
- TestStop — verifies POST /stop
- TestHealth — verifies response parsing
- TestSpeakConnectionError — unreachable URL returns sensible error

### pkg/vdclient/socket_test.go — socket client tests

Test with a temporary Unix socket (os.CreateTemp + net.Listen "unix"):
- TestDialAndStatus — connect, send status, verify response
- TestStartStop — start session, push fake transcripts, stop, verify accumulated text
- TestTranscriptChannel — start, push 3 transcript lines, verify channel receives all 3 in order
- TestCancel — start, cancel, verify "cancelled"
- TestClose — connect, close, verify clean shutdown

### pkg/vdclient/example_test.go — runnable examples

```go
func ExampleClient_Speak() {
    client := vdclient.New("http://localhost:5111")
    resp, err := client.Speak(context.Background(), vdclient.SpeakRequest{
        Text:    "Hello from osog",
        Backend: "speaches",
        Voice:   "af_heart",
    })
    // ...
}

func ExampleDial() {
    session, err := vdclient.Dial("/tmp/voice-daemon.sock")
    defer session.Close()
    
    session.Start()
    for transcript := range session.Transcripts() {
        fmt.Println("Heard:", transcript)
    }
    full, _ := session.Stop()
    fmt.Println("Full:", full)
}
```

## Task 2: docs/integration.md — Consumer Guide

Write `docs/integration.md` covering:

### Quick Start (Go)
```go
import "github.com/realnikolaj/voicedaemon/pkg/vdclient"
```
- Create client, speak, health check
- Dial socket, start/stop session, read transcripts

### Quick Start (Any Language)
- HTTP API: POST /speak, POST /stop, GET /health with curl examples
- Unix socket: line protocol, transcript push format

### API Reference
- HTTP endpoints: method, path, request body, response body, status codes
- Socket protocol: commands, responses, push messages, state machine diagram

### Architecture Notes for Consumers
- voicedaemon must be running before your app starts
- One STT session at a time (socket is single-session)
- TTS queue is FIFO — multiple Speak() calls queue sequentially
- Health endpoint is the connectivity check — call it on startup
- Backend switching: "speaches" vs "pockettts" per request
- Voice selection: pass voice ID in SpeakRequest

### Common Patterns
- "Check then use": Health() on startup, fail fast with helpful error if daemon not running
- "Fire and forget TTS": Speak() returns when queued, not when playback finishes
- "Stream transcripts": Use Transcripts() channel in a goroutine, process as they arrive
- "Accumulate then use": Start(), ignore channel, Stop() returns full joined transcript

## Validation
```bash
go build ./...                           # including new pkg/
go test -race ./pkg/vdclient/...         # client tests pass
go vet ./pkg/vdclient/...               # clean
make check-noapm                         # full gate still passes
```

## Files Created
- `pkg/vdclient/client.go`
- `pkg/vdclient/socket.go`
- `pkg/vdclient/types.go`
- `pkg/vdclient/client_test.go`
- `pkg/vdclient/socket_test.go`
- `pkg/vdclient/example_test.go`
- `docs/integration.md`

## Files NOT Modified
- Everything in `internal/` — untouched
- `cmd/voicedaemon/` — untouched
- `Makefile` — the existing `go test ./...` already picks up `pkg/`

## Notes
- Use `net/http` for the HTTP client (stdlib, no external deps in pkg/)
- Use `net` for Unix socket (stdlib)
- Use `encoding/json` for marshal/unmarshal
- Zero external dependencies in pkg/vdclient — consumers don't inherit our dep tree
- `context.Context` on all HTTP methods for cancellation support
- Socket methods don't take context (blocking I/O on Unix socket, context cancellation is complex)
- The Transcripts() channel pattern is idiomatic Go for streaming data from a goroutine
