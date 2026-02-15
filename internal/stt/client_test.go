package stt

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTranscribe(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		wantText   string
		wantErr    bool
	}{
		{
			name:       "successful transcription",
			statusCode: http.StatusOK,
			response:   `{"text": "hello world"}`,
			wantText:   "hello world",
		},
		{
			name:       "empty transcription",
			statusCode: http.StatusOK,
			response:   `{"text": ""}`,
			wantText:   "",
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			response:   "internal error",
			wantErr:    true,
		},
		{
			name:       "bad json response",
			statusCode: http.StatusOK,
			response:   "not json",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/v1/audio/transcriptions" {
					t.Errorf("path = %s, want /v1/audio/transcriptions", r.URL.Path)
				}

				ct := r.Header.Get("Content-Type")
				if ct == "" {
					t.Error("missing Content-Type header")
				}

				// Parse multipart to verify form fields
				if err := r.ParseMultipartForm(10 << 20); err != nil {
					t.Errorf("parse multipart: %v", err)
				}

				model := r.FormValue("model")
				if model == "" {
					t.Error("missing model field")
				}

				language := r.FormValue("language")
				if language == "" {
					t.Error("missing language field")
				}

				file, header, err := r.FormFile("file")
				if err != nil {
					t.Errorf("get form file: %v", err)
				} else {
					file.Close()
					if header.Filename != "audio.wav" {
						t.Errorf("filename = %s, want audio.wav", header.Filename)
					}
				}

				w.WriteHeader(tt.statusCode)
				if _, err := w.Write([]byte(tt.response)); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer srv.Close()

			cfg := DefaultClientConfig()
			cfg.URL = srv.URL
			client := NewClient(cfg)

			// Generate 1 second of 48kHz sine wave
			samples := make([]float32, 48000)
			for i := range samples {
				samples[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0))
			}

			text, err := client.Transcribe(context.Background(), samples)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestPeakNormalize(t *testing.T) {
	tests := []struct {
		name    string
		input   []float32
		wantMax float32
	}{
		{
			name:    "quiet signal",
			input:   []float32{0.1, -0.2, 0.15, -0.05},
			wantMax: 1.0,
		},
		{
			name:    "already normalized",
			input:   []float32{1.0, -0.5, 0.3},
			wantMax: 1.0,
		},
		{
			name:    "silence",
			input:   []float32{0, 0, 0},
			wantMax: 0,
		},
		{
			name:    "empty",
			input:   []float32{},
			wantMax: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := peakNormalize(tt.input)

			var maxAbs float32
			for _, s := range out {
				abs := float32(math.Abs(float64(s)))
				if abs > maxAbs {
					maxAbs = abs
				}
			}

			if tt.wantMax == 0 {
				if maxAbs > 1e-6 {
					t.Errorf("expected silence, got max=%f", maxAbs)
				}
			} else {
				if math.Abs(float64(maxAbs-tt.wantMax)) > 0.01 {
					t.Errorf("max amplitude = %f, want ~%f", maxAbs, tt.wantMax)
				}
			}
		})
	}
}

func TestEncodeWAV(t *testing.T) {
	samples := make([]float32, 1600)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / 16000.0))
	}

	wav, err := encodeWAV(samples, 16000)
	if err != nil {
		t.Fatalf("encodeWAV: %v", err)
	}

	// Check RIFF header
	if string(wav[0:4]) != "RIFF" {
		t.Errorf("missing RIFF header")
	}
	if string(wav[8:12]) != "WAVE" {
		t.Errorf("missing WAVE format")
	}

	// Check expected size: 44 header + 1600*2 bytes = 3244
	expectedSize := 44 + len(samples)*2
	if len(wav) != expectedSize {
		t.Errorf("wav size = %d, want %d", len(wav), expectedSize)
	}
}

func TestTranscribeRequestFormat(t *testing.T) {
	var receivedFields map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("parse: %v", err)
		}
		receivedFields = map[string]string{
			"model":    r.FormValue("model"),
			"language": r.FormValue("language"),
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"text": "ok"}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	cfg := DefaultClientConfig()
	cfg.URL = srv.URL
	cfg.Model = "test-model"
	cfg.Language = "de"
	client := NewClient(cfg)

	_, err := client.Transcribe(context.Background(), make([]float32, 480))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedFields["model"] != "test-model" {
		t.Errorf("model = %q, want %q", receivedFields["model"], "test-model")
	}
	if receivedFields["language"] != "de" {
		t.Errorf("language = %q, want %q", receivedFields["language"], "de")
	}
}
