package vdclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is an HTTP client for the voicedaemon TTS and health APIs.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new voicedaemon HTTP client.
// baseURL is the daemon's HTTP address, e.g. "http://localhost:5111".
func New(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

// Speak queues text for TTS playback. Blocks until accepted by the daemon
// (not until playback finishes).
func (c *Client) Speak(ctx context.Context, req SpeakRequest) (*SpeakResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("vdclient: marshal speak request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/speak", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vdclient: create speak request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vdclient: speak request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp)
	}

	var out SpeakResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("vdclient: decode speak response: %w", err)
	}
	return &out, nil
}

// Stop cancels current playback and clears the TTS queue.
func (c *Client) Stop(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/stop", nil)
	if err != nil {
		return fmt.Errorf("vdclient: create stop request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("vdclient: stop request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp)
	}
	return nil
}

// Health checks daemon connectivity and returns status.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("vdclient: create health request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vdclient: health request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp)
	}

	var out HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("vdclient: decode health response: %w", err)
	}
	return &out, nil
}

// SetVADThreshold sets the VAD silence detection threshold on the daemon.
func (c *Client) SetVADThreshold(ctx context.Context, threshold float64) error {
	body, err := json.Marshal(map[string]float64{"threshold": threshold})
	if err != nil {
		return fmt.Errorf("vdclient: marshal threshold request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/vad/threshold", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vdclient: create threshold request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("vdclient: threshold request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp)
	}
	return nil
}

// SetMicMute enables or disables mic muting on the daemon.
func (c *Client) SetMicMute(ctx context.Context, muted bool) error {
	body, err := json.Marshal(map[string]bool{"muted": muted})
	if err != nil {
		return fmt.Errorf("vdclient: marshal mute request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/mic/mute", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vdclient: create mute request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("vdclient: mute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp)
	}
	return nil
}

// SetGain sets the input gain multiplier on the daemon.
func (c *Client) SetGain(ctx context.Context, gain float64) error {
	body, err := json.Marshal(map[string]float64{"gain": gain})
	if err != nil {
		return fmt.Errorf("vdclient: marshal gain request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/gain", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("vdclient: create gain request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("vdclient: gain request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp)
	}
	return nil
}

// Config returns the current daemon runtime configuration.
func (c *Client) Config(ctx context.Context) (*ConfigResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/config", nil)
	if err != nil {
		return nil, fmt.Errorf("vdclient: create config request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vdclient: config request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp)
	}

	var out ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("vdclient: decode config response: %w", err)
	}
	return &out, nil
}

// TranscriptStream opens an SSE connection to the daemon's transcript stream.
// Returns a channel that receives transcript text as it arrives. The channel is
// closed when the context is canceled or the connection drops.
func (c *Client) TranscriptStream(ctx context.Context) (<-chan string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/transcripts/stream", nil)
	if err != nil {
		return nil, fmt.Errorf("vdclient: create transcript stream request: %w", err)
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vdclient: transcript stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.readError(resp)
	}

	ch := make(chan string, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			text := strings.TrimPrefix(line, "data: ")
			select {
			case ch <- text:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

func (c *Client) readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("vdclient: HTTP %d: %s", resp.StatusCode, string(body))
}
