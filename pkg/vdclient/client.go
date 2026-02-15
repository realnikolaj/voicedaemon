package vdclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func (c *Client) readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("vdclient: HTTP %d: %s", resp.StatusCode, string(body))
}
