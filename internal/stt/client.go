package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/realnikolaj/voicedaemon/internal/audio"
)

// ClientConfig holds configuration for the STT client.
type ClientConfig struct {
	URL      string
	Model    string
	Language string
	Timeout  time.Duration
	Logf     func(string, ...any)

	// VAD parameters passed to Speaches server-side Silero VAD.
	// These only apply when the server has _UNSTABLE_VAD_FILTER=True.
	VADThreshold          float64 // Speech probability threshold (0-1, default 0.5)
	VADMinSilenceDuration int     // Min silence between segments in ms (default 160)
	VADMaxSpeechDuration  float64 // Max speech chunk duration in seconds (default 30)
	VADSpeechPad          int     // Padding around speech segments in ms (default 400)
}

// DefaultClientConfig returns STT client config with standard defaults.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		URL:      "http://localhost:34331",
		Model:    "deepdml/faster-whisper-large-v3-turbo-ct2",
		Language: "en",
		Timeout:  30 * time.Second,
	}
}

// Client is a Speaches STT HTTP client.
type Client struct {
	cfg    ClientConfig
	logf   func(string, ...any)
	client *http.Client
}

// NewClient creates a new STT client.
func NewClient(cfg ClientConfig) *Client {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &Client{
		cfg:  cfg,
		logf: logf,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// transcriptionResponse is the JSON response from Speaches STT.
type transcriptionResponse struct {
	Text string `json:"text"`
}

// Transcribe sends audio to the STT service and returns the transcript.
// Input audio is float32 at 48kHz; it gets resampled to 16kHz and WAV-encoded.
func (c *Client) Transcribe(ctx context.Context, samples []float32) (string, error) {
	// Normalize audio (peak normalize)
	normalized := peakNormalize(samples)

	// Resample from 48kHz to 16kHz for Whisper
	resampled := audio.Resample(normalized, audio.SampleRate, audio.STTSampleRate)

	// Encode as WAV
	wavData, err := encodeWAV(resampled, audio.STTSampleRate)
	if err != nil {
		return "", fmt.Errorf("stt: encode wav: %w", err)
	}

	// Build multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("stt: create form file: %w", err)
	}
	if _, err := part.Write(wavData); err != nil {
		return "", fmt.Errorf("stt: write wav data: %w", err)
	}

	if err := writer.WriteField("model", c.cfg.Model); err != nil {
		return "", fmt.Errorf("stt: write model field: %w", err)
	}
	if err := writer.WriteField("language", c.cfg.Language); err != nil {
		return "", fmt.Errorf("stt: write language field: %w", err)
	}

	// Server-side VAD parameters (Speaches Silero VAD v5)
	if c.cfg.VADThreshold > 0 {
		writer.WriteField("vad_threshold", fmt.Sprintf("%.2f", c.cfg.VADThreshold))
	}
	if c.cfg.VADMinSilenceDuration > 0 {
		writer.WriteField("min_silence_duration_ms", fmt.Sprintf("%d", c.cfg.VADMinSilenceDuration))
	}
	if c.cfg.VADMaxSpeechDuration > 0 {
		writer.WriteField("max_speech_duration_s", fmt.Sprintf("%.1f", c.cfg.VADMaxSpeechDuration))
	}
	if c.cfg.VADSpeechPad > 0 {
		writer.WriteField("speech_pad_ms", fmt.Sprintf("%d", c.cfg.VADSpeechPad))
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("stt: close multipart writer: %w", err)
	}

	url := c.cfg.URL + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("stt: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	c.logf("stt: transcribing %d samples (%.1fs audio)", len(resampled), float64(len(resampled))/float64(audio.STTSampleRate))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("stt: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result transcriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("stt: decode response: %w", err)
	}

	c.logf("stt: transcript: %q", result.Text)
	return result.Text, nil
}

// peakNormalize scales audio so the peak amplitude is 1.0.
func peakNormalize(samples []float32) []float32 {
	if len(samples) == 0 {
		return samples
	}

	var peak float32
	for _, s := range samples {
		abs := float32(math.Abs(float64(s)))
		if abs > peak {
			peak = abs
		}
	}

	if peak < 1e-6 {
		return samples
	}

	out := make([]float32, len(samples))
	scale := 1.0 / peak
	for i, s := range samples {
		out[i] = s * scale
	}
	return out
}

// encodeWAV encodes float32 samples as a 16-bit PCM WAV file.
func encodeWAV(samples []float32, sampleRate int) ([]byte, error) {
	numSamples := len(samples)
	dataSize := numSamples * 2
	fileSize := 44 + dataSize

	buf := make([]byte, fileSize)

	// RIFF header
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(fileSize-8))
	copy(buf[8:12], "WAVE")

	// fmt chunk
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(buf[22:24], 1)  // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2)) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                    // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                   // bits per sample

	// data chunk
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	for i, s := range samples {
		// Clamp to [-1, 1]
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		v := int16(s * 32767)
		binary.LittleEndian.PutUint16(buf[44+i*2:44+i*2+2], uint16(v))
	}

	return buf, nil
}
