package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Backend identifies a TTS backend.
type Backend string

const (
	BackendSpeaches Backend = "speaches"
	BackendPocket   Backend = "pocket"
)

// Known model → sample rate mappings.
var modelSampleRates = map[string]int{
	"speaches-ai/Kokoro-82M-v1.0-ONNX": 24000,
	"kokoro":                           24000,
	"piper":                            22050,
	"glados":                           22050,
}

// ClientConfig holds configuration for the TTS client.
type ClientConfig struct {
	SpeachesURL   string
	PocketTTSURL  string
	SpeachesModel string
	SpeachesVoice string
	PocketVoice   string
	Logf          func(string, ...any)
}

// DefaultClientConfig returns TTS client config with standard defaults.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		SpeachesURL:   "http://localhost:34331",
		PocketTTSURL:  "http://localhost:49112",
		SpeachesModel: "speaches-ai/Kokoro-82M-v1.0-ONNX",
		SpeachesVoice: "af_heart",
		PocketVoice:   "alba",
	}
}

// StreamOpts provides per-request overrides for TTS streaming.
type StreamOpts struct {
	Model string
	Voice string
}

// Client is a unified TTS streaming client supporting Speaches and PocketTTS.
type Client struct {
	cfg    ClientConfig
	logf   func(string, ...any)
	client *http.Client
}

// NewClient creates a new TTS client.
func NewClient(cfg ClientConfig) *Client {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &Client{
		cfg:    cfg,
		logf:   logf,
		client: &http.Client{},
	}
}

// SampleRateForModel returns the expected output sample rate for a model.
// Defaults to 24000 for unknown models.
func SampleRateForModel(model string) int {
	if rate, ok := modelSampleRates[model]; ok {
		return rate
	}
	return 24000
}

// ttsRequest is the JSON body for the TTS API.
type ttsRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
	Stream         bool   `json:"stream,omitempty"`
}

// Stream sends text to a TTS backend and returns a channel of raw PCM s16le chunks.
// The channel is closed when streaming is complete or the context is canceled.
func (c *Client) Stream(ctx context.Context, text string, backend Backend, opts *StreamOpts) (<-chan []byte, int, error) {
	var url, model, voice string

	switch backend {
	case BackendSpeaches:
		url = c.cfg.SpeachesURL + "/v1/audio/speech"
		model = c.cfg.SpeachesModel
		voice = c.cfg.SpeachesVoice
	case BackendPocket:
		url = c.cfg.PocketTTSURL + "/v1/audio/speech"
		model = "piper"
		voice = c.cfg.PocketVoice
	default:
		return nil, 0, fmt.Errorf("tts: unknown backend: %s", backend)
	}

	if opts != nil {
		if opts.Model != "" {
			model = opts.Model
		}
		if opts.Voice != "" {
			voice = opts.Voice
		}
	}

	sampleRate := SampleRateForModel(model)

	reqBody := ttsRequest{
		Model:          model,
		Input:          text,
		Voice:          voice,
		ResponseFormat: "pcm",
	}

	if backend == BackendPocket {
		reqBody.Stream = true
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("tts: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("tts: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	c.logf("tts: streaming %q via %s (model=%s, voice=%s, rate=%d)", text, backend, model, voice, sampleRate)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("tts: request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, 0, fmt.Errorf("tts: status %d: %s", resp.StatusCode, string(respBody))
	}

	chunks := make(chan []byte, 64)

	go func() {
		defer resp.Body.Close()
		defer close(chunks)

		buf := make([]byte, 4096)
		var carry byte
		hasCarry := false

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := resp.Body.Read(buf)
			if n > 0 {
				var chunk []byte
				if hasCarry {
					chunk = make([]byte, n+1)
					chunk[0] = carry
					copy(chunk[1:], buf[:n])
					hasCarry = false
				} else {
					chunk = make([]byte, n)
					copy(chunk, buf[:n])
				}

				// PCM s16le: each sample is 2 bytes. If chunk has odd length,
				// save the trailing byte for the next read to avoid splitting
				// a sample across chunk boundaries.
				if len(chunk)%2 != 0 {
					carry = chunk[len(chunk)-1]
					hasCarry = true
					chunk = chunk[:len(chunk)-1]
				}

				if len(chunk) > 0 {
					select {
					case chunks <- chunk:
					case <-ctx.Done():
						return
					}
				}
			}

			if err != nil {
				if err != io.EOF {
					c.logf("tts: stream read error: %v", err)
				}
				return
			}
		}
	}()

	return chunks, sampleRate, nil
}

// SpeachesURL returns the configured Speaches URL.
func (c *Client) SpeachesURL() string {
	return c.cfg.SpeachesURL
}

// PocketTTSURL returns the configured PocketTTS URL.
func (c *Client) PocketTTSURL() string {
	return c.cfg.PocketTTSURL
}
