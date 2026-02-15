package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStream(t *testing.T) {
	tests := []struct {
		name       string
		backend    Backend
		text       string
		statusCode int
		response   []byte
		wantErr    bool
		wantChunks bool
	}{
		{
			name:       "speaches streaming",
			backend:    BackendSpeaches,
			text:       "hello world",
			statusCode: http.StatusOK,
			response:   make([]byte, 4800), // 100ms of 24kHz s16le
			wantChunks: true,
		},
		{
			name:       "pocket streaming",
			backend:    BackendPocket,
			text:       "hello world",
			statusCode: http.StatusOK,
			response:   make([]byte, 4800),
			wantChunks: true,
		},
		{
			name:       "server error",
			backend:    BackendSpeaches,
			text:       "hello",
			statusCode: http.StatusInternalServerError,
			response:   []byte("error"),
			wantErr:    true,
		},
		{
			name:    "unknown backend",
			backend: Backend("unknown"),
			text:    "hello",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.backend == Backend("unknown") {
				cfg := DefaultClientConfig()
				client := NewClient(cfg)
				_, _, err := client.Stream(context.Background(), tt.text, tt.backend, nil)
				if err == nil {
					t.Error("expected error for unknown backend")
				}
				return
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/v1/audio/speech" {
					t.Errorf("path = %s, want /v1/audio/speech", r.URL.Path)
				}

				ct := r.Header.Get("Content-Type")
				if ct != "application/json" {
					t.Errorf("content-type = %s, want application/json", ct)
				}

				var req ttsRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
				}

				if req.Input != tt.text {
					t.Errorf("input = %q, want %q", req.Input, tt.text)
				}
				if req.ResponseFormat != "pcm" {
					t.Errorf("response_format = %q, want pcm", req.ResponseFormat)
				}

				if tt.backend == BackendPocket && !req.Stream {
					t.Error("pocket request missing stream=true")
				}

				w.WriteHeader(tt.statusCode)
				if _, err := w.Write(tt.response); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer srv.Close()

			cfg := DefaultClientConfig()
			cfg.SpeachesURL = srv.URL
			cfg.PocketTTSURL = srv.URL
			client := NewClient(cfg)

			chunks, sampleRate, err := client.Stream(context.Background(), tt.text, tt.backend, nil)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if sampleRate <= 0 {
				t.Errorf("sample rate = %d, want > 0", sampleRate)
			}

			if tt.wantChunks {
				totalBytes := 0
				for chunk := range chunks {
					totalBytes += len(chunk)
				}
				if totalBytes == 0 {
					t.Error("received no data from stream")
				}
			}
		})
	}
}

func TestSampleRateForModel(t *testing.T) {
	tests := []struct {
		model    string
		wantRate int
	}{
		{"speaches-ai/Kokoro-82M-v1.0-ONNX", 24000},
		{"kokoro", 24000},
		{"piper", 22050},
		{"glados", 22050},
		{"unknown-model", 24000},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			rate := SampleRateForModel(tt.model)
			if rate != tt.wantRate {
				t.Errorf("SampleRateForModel(%q) = %d, want %d", tt.model, rate, tt.wantRate)
			}
		})
	}
}

func TestStreamWithOpts(t *testing.T) {
	var receivedModel, receivedVoice string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		receivedModel = req.Model
		receivedVoice = req.Voice
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(make([]byte, 100)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer srv.Close()

	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	opts := &StreamOpts{
		Model: "custom-model",
		Voice: "custom-voice",
	}
	chunks, _, err := client.Stream(context.Background(), "test", BackendSpeaches, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain channel
	for range chunks {
	}

	if receivedModel != "custom-model" {
		t.Errorf("model = %q, want custom-model", receivedModel)
	}
	if receivedVoice != "custom-voice" {
		t.Errorf("voice = %q, want custom-voice", receivedVoice)
	}
}

func TestStreamCancelation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write slowly — context should cancel before we finish
		for range 100 {
			if _, err := w.Write(make([]byte, 1000)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	chunks, _, err := client.Stream(ctx, "test", BackendSpeaches, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read one chunk then cancel
	<-chunks
	cancel()

	// Channel should eventually close
	for range chunks {
	}
}
