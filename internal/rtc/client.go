package rtc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

const (
	dataChannelLabel     = "oai-events"
	vadThreshold         = 0.9
	vadSilenceDurationMs = 1500
	dcOpenTimeout        = 10 * time.Second
)

// ClientConfig holds configuration for the WebRTC realtime STT client.
type ClientConfig struct {
	SpeachesURL string
	Model       string
	Language    string
	Logf        func(string, ...any)
}

// Client manages a single WebRTC session with the Speaches realtime endpoint.
// Audio is streamed via an Opus track; transcripts arrive over the oai-events
// data channel. Call Connect to establish the session, then feed audio frames
// via SendAudio. Transcripts are available on the channel returned by Transcripts.
type Client struct {
	cfg  ClientConfig
	logf func(string, ...any)

	pc         *webrtc.PeerConnection
	dc         *webrtc.DataChannel
	audioTrack *webrtc.TrackLocalStaticSample
	enc        *opusEncoder
	asm        *reassembler

	transcripts chan string

	mu     sync.Mutex
	closed bool
}

// NewClient returns a Client that is ready to connect.
func NewClient(cfg ClientConfig) *Client {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Client{
		cfg:         cfg,
		logf:        logf,
		asm:         newReassembler(),
		transcripts: make(chan string, 64),
	}
}

// Connect establishes the WebRTC session. It blocks until the data channel
// is open and the session.update has been sent, or until ctx is cancelled.
func (c *Client) Connect(ctx context.Context) error {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return fmt.Errorf("rtc: peer connection: %w", err)
	}

	// Opus audio track — 48kHz stereo, required by aiortc on the server.
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "voicedaemon-mic",
	)
	if err != nil {
		pc.Close()
		return fmt.Errorf("rtc: audio track: %w", err)
	}
	if _, err := pc.AddTrack(audioTrack); err != nil {
		pc.Close()
		return fmt.Errorf("rtc: add track: %w", err)
	}

	enc, err := newOpusEncoder()
	if err != nil {
		pc.Close()
		return fmt.Errorf("rtc: opus encoder: %w", err)
	}

	// Data channel — must be created by the offerer (us).
	dc, err := pc.CreateDataChannel(dataChannelLabel, nil)
	if err != nil {
		pc.Close()
		return fmt.Errorf("rtc: data channel: %w", err)
	}

	dcOpen := make(chan struct{}, 1)
	dc.OnOpen(func() {
		c.logf("rtc: data channel open")
		select {
		case dcOpen <- struct{}{}:
		default:
		}
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		c.handleMessage(msg.Data)
	})
	dc.OnClose(func() { c.logf("rtc: data channel closed") })

	// Build offer.
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return fmt.Errorf("rtc: create offer: %w", err)
	}

	// Wait for full ICE gathering before sending SDP — aiortc has no trickle ICE.
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return fmt.Errorf("rtc: set local description: %w", err)
	}
	select {
	case <-gatherDone:
	case <-ctx.Done():
		pc.Close()
		return ctx.Err()
	}

	sdpAnswer, err := c.postSDP(ctx, pc.LocalDescription().SDP)
	if err != nil {
		pc.Close()
		return err
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdpAnswer,
	}); err != nil {
		pc.Close()
		return fmt.Errorf("rtc: set remote description: %w", err)
	}

	select {
	case <-dcOpen:
	case <-time.After(dcOpenTimeout):
		pc.Close()
		return fmt.Errorf("rtc: timeout waiting for data channel to open")
	case <-ctx.Done():
		pc.Close()
		return ctx.Err()
	}

	if err := c.sendSessionUpdate(dc); err != nil {
		pc.Close()
		return fmt.Errorf("rtc: session.update: %w", err)
	}

	c.mu.Lock()
	c.pc = pc
	c.dc = dc
	c.audioTrack = audioTrack
	c.enc = enc
	c.mu.Unlock()

	c.logf("rtc: connected (model=%s, vad_threshold=%.1f, vad_silence=%dms)",
		c.cfg.Model, vadThreshold, vadSilenceDurationMs)
	return nil
}

// SendAudio accepts a mono 480-sample (10ms at 48kHz) float32 frame,
// encodes it as Opus stereo, and writes it to the WebRTC audio track.
func (c *Client) SendAudio(mono []float32) error {
	c.mu.Lock()
	track := c.audioTrack
	enc := c.enc
	c.mu.Unlock()

	if track == nil || enc == nil {
		return nil
	}
	return enc.writeTo(mono, track)
}

// Transcripts returns a channel on which completed transcripts are sent.
// The channel is never closed — callers should stop reading when Close is called.
func (c *Client) Transcripts() <-chan string {
	return c.transcripts
}

// Close tears down the peer connection and frees resources.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if c.enc != nil {
		c.enc.close()
		c.enc = nil
	}
	if c.pc != nil {
		err := c.pc.Close()
		c.pc = nil
		c.audioTrack = nil
		c.dc = nil
		return err
	}
	return nil
}

// postSDP sends the SDP offer to Speaches and returns the SDP answer text.
func (c *Client) postSDP(ctx context.Context, sdpOffer string) (string, error) {
	url := c.cfg.SpeachesURL + "/v1/realtime?model=" + c.cfg.Model
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(sdpOffer))
	if err != nil {
		return "", fmt.Errorf("rtc: build SDP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/sdp")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("rtc: POST /v1/realtime: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("rtc: POST /v1/realtime: HTTP %d: %s", resp.StatusCode, body)
	}
	return string(body), nil
}

// sendSessionUpdate configures transcription-only mode with server-side VAD.
func (c *Client) sendSessionUpdate(dc *webrtc.DataChannel) error {
	msg := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"input_audio_transcription": map[string]any{
				"model":    c.cfg.Model,
				"language": c.cfg.Language,
			},
			"turn_detection": map[string]any{
				"type":                "server_vad",
				"threshold":           vadThreshold,
				"silence_duration_ms": vadSilenceDurationMs,
				"create_response":     false,
			},
		},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return dc.SendText(string(b))
}

// handleMessage decodes one data channel message and forwards any transcript.
func (c *Client) handleMessage(raw []byte) {
	payload, err := c.asm.feed(raw)
	if err != nil {
		c.logf("rtc: fragment error: %v", err)
		return
	}
	if payload == nil {
		return
	}
	text := extractTranscript(payload)
	if text == "" {
		return
	}
	c.logf("rtc: transcript: %q", text)
	select {
	case c.transcripts <- text:
	default:
		c.logf("rtc: transcript channel full, dropping")
	}
}
