package rtc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultVADThreshold    = 0.9
	defaultVADSilenceDurMs = 1500
	wsSampleRate           = 24000 // OpenAI realtime API expects 24kHz
	audioChunkFrames       = 10    // accumulate 10 portaudio frames before sending
	audioChunkBytes        = 240 * 2 * audioChunkFrames // 240 samples/frame at 24kHz × 2 bytes × 10 frames = 4800
)

// ClientConfig holds configuration for the WebSocket realtime STT client.
type ClientConfig struct {
	SpeachesURL  string
	Model        string
	Language     string
	VADThreshold float64
	VADSilenceMs int
	Logf         func(string, ...any)
}

// Client manages a WebSocket session with the Speaches realtime endpoint.
// Audio is sent as base64-encoded 24kHz PCM via input_audio_buffer.append.
// Transcripts arrive as conversation.item.input_audio_transcription.completed.
type Client struct {
	cfg  ClientConfig
	logf func(string, ...any)

	conn    *websocket.Conn
	writeMu sync.Mutex // serialise WebSocket writes

	audioBuf [audioChunkBytes]byte // accumulation buffer for 24kHz PCM
	audioPos int                    // write position in audioBuf

	transcripts chan string

	mu     sync.Mutex
	closed bool
}

// NewClient returns a Client ready to connect.
func NewClient(cfg ClientConfig) *Client {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Client{
		cfg:         cfg,
		logf:        logf,
		transcripts: make(chan string, 64),
	}
}

// Connect establishes the WebSocket session. Blocks until connected and
// session.update is sent, or until ctx is cancelled.
func (c *Client) Connect(ctx context.Context) error {
	wsURL := strings.Replace(c.cfg.SpeachesURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL += "/v1/realtime?model=" + c.cfg.Model + "&intent=transcription"

	c.logf("rtc: dialing %s", wsURL)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("rtc: websocket dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	if err := c.sendSessionUpdate(); err != nil {
		conn.Close()
		return fmt.Errorf("rtc: session.update: %w", err)
	}

	go c.readLoop()

	t := c.cfg.VADThreshold
	if t <= 0 { t = defaultVADThreshold }
	s := c.cfg.VADSilenceMs
	if s <= 0 { s = defaultVADSilenceDurMs }
	c.logf("rtc: connected (model=%s, vad=%.2f, silence=%dms)", c.cfg.Model, t, s)
	return nil
}

// SendAudio accepts a mono 480-sample (10ms at 48kHz) float32 frame.
// Frames are accumulated into ~100ms chunks before sending to reduce
// WebSocket message rate from 100/s to 10/s.
func (c *Client) SendAudio(mono []float32) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil
	}

	// Resample 48kHz → 24kHz (drop every other sample) and convert to int16.
	n24 := len(mono) / 2
	for i := 0; i < n24; i++ {
		s := mono[i*2]
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		binary.LittleEndian.PutUint16(c.audioBuf[c.audioPos:], uint16(int16(s*math.MaxInt16)))
		c.audioPos += 2
	}

	// Flush every 10 frames (~100ms = 2400 samples at 24kHz = 4800 bytes).
	if c.audioPos < audioChunkBytes {
		return nil
	}

	b64 := base64.StdEncoding.EncodeToString(c.audioBuf[:c.audioPos])
	c.audioPos = 0

	msg, _ := json.Marshal(map[string]string{
		"type":  "input_audio_buffer.append",
		"audio": b64,
	})

	c.writeMu.Lock()
	err := conn.WriteMessage(websocket.TextMessage, msg)
	c.writeMu.Unlock()
	return err
}

// Transcripts returns the channel on which completed transcripts are delivered.
func (c *Client) Transcripts() <-chan string {
	return c.transcripts
}

// Close tears down the WebSocket connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// sendSessionUpdate configures transcription-only mode with server-side VAD.
func (c *Client) sendSessionUpdate() error {
	threshold := c.cfg.VADThreshold
	if threshold <= 0 {
		threshold = defaultVADThreshold
	}
	silenceMs := c.cfg.VADSilenceMs
	if silenceMs <= 0 {
		silenceMs = defaultVADSilenceDurMs
	}

	msg := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"input_audio_transcription": map[string]any{
				"model":    c.cfg.Model,
				"language": c.cfg.Language,
			},
			"turn_detection": map[string]any{
				"type":                "server_vad",
				"threshold":           threshold,
				"silence_duration_ms": silenceMs,
				"create_response":     false,
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// readLoop reads WebSocket messages and extracts transcripts.
func (c *Client) readLoop() {
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if !closed {
				c.logf("rtc: websocket read: %v", err)
			}
			return
		}

		// Log event type for debugging.
		var ev struct{ Type string `json:"type"` }
		if json.Unmarshal(message, &ev) == nil && ev.Type != "" {
			switch ev.Type {
			case "input_audio_buffer.speech_started":
				c.logf("rtc: speech started")
			case "input_audio_buffer.speech_stopped":
				c.logf("rtc: speech stopped")
			case "input_audio_buffer.committed":
				c.logf("rtc: buffer committed")
			case "conversation.item.input_audio_transcription.completed":
				// handled below
			case "session.created", "session.updated":
				c.logf("rtc: %s", ev.Type)
			case "error":
				c.logf("rtc: server error: %s", string(message))
			default:
				c.logf("rtc: event: %s", ev.Type)
			}
		}

		text := extractTranscript(message)
		if text == "" {
			continue
		}
		c.logf("rtc: transcript: %q", text)
		select {
		case c.transcripts <- text:
		default:
			c.logf("rtc: transcript channel full, dropping")
		}
	}
}
