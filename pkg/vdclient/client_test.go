package vdclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSpeak(t *testing.T) {
	tests := []struct {
		name     string
		req      SpeakRequest
		wantBody map[string]any
	}{
		{
			name: "basic text",
			req:  SpeakRequest{Text: "hello"},
			wantBody: map[string]any{
				"text": "hello",
			},
		},
		{
			name: "with voice and backend",
			req:  SpeakRequest{Text: "hi", Backend: "pocket", Voice: "alba"},
			wantBody: map[string]any{
				"text":    "hi",
				"backend": "pocket",
				"voice":   "alba",
			},
		},
		{
			name: "with model override",
			req:  SpeakRequest{Text: "test", Model: "custom-model", Voice: "alice"},
			wantBody: map[string]any{
				"text":  "test",
				"model": "custom-model",
				"voice": "alice",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/speak" {
					t.Errorf("path = %s, want /speak", r.URL.Path)
				}

				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				for k, want := range tt.wantBody {
					if got[k] != want {
						t.Errorf("body[%q] = %v, want %v", k, got[k], want)
					}
				}

				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(SpeakResponse{
					Status:     "queued",
					QueueDepth: 1,
					Backend:    "speaches",
				}); err != nil {
					t.Fatalf("encode response: %v", err)
				}
			}))
			defer srv.Close()

			client := New(srv.URL)
			resp, err := client.Speak(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Speak() error: %v", err)
			}
			if resp.Status != "queued" {
				t.Errorf("status = %q, want %q", resp.Status, "queued")
			}
			if resp.QueueDepth != 1 {
				t.Errorf("queue_depth = %d, want 1", resp.QueueDepth)
			}
		})
	}
}

func TestStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/stop" {
			t.Errorf("path = %s, want /stop", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "stopped"}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer srv.Close()

	client := New(srv.URL)
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
}

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/health" {
			t.Errorf("path = %s, want /health", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(HealthResponse{
			Status:       "ok",
			QueueDepth:   0,
			SpeachesURL:  "http://localhost:34331",
			PocketTTSURL: "http://localhost:49112",
			STTURL:       "http://localhost:34331",
			STTSocket:    "/tmp/voice-daemon.sock",
		}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer srv.Close()

	client := New(srv.URL)
	resp, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.SpeachesURL != "http://localhost:34331" {
		t.Errorf("speaches_url = %q, want %q", resp.SpeachesURL, "http://localhost:34331")
	}
	if resp.STTSocket != "/tmp/voice-daemon.sock" {
		t.Errorf("stt_socket = %q, want %q", resp.STTSocket, "/tmp/voice-daemon.sock")
	}
}

func TestSpeakConnectionError(t *testing.T) {
	client := New("http://127.0.0.1:1") // unreachable
	_, err := client.Speak(context.Background(), SpeakRequest{Text: "test"})
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestSpeakHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"error":"text is required"}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}))
	defer srv.Close()

	client := New(srv.URL)
	_, err := client.Speak(context.Background(), SpeakRequest{})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestSetVADThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
	}{
		{"low threshold", 0.01},
		{"high threshold", 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/vad/threshold" {
					t.Errorf("path = %s, want /vad/threshold", r.URL.Path)
				}

				var got map[string]float64
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got["threshold"] != tt.threshold {
					t.Errorf("threshold = %f, want %f", got["threshold"], tt.threshold)
				}

				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "threshold": got["threshold"]}); err != nil {
					t.Fatalf("encode: %v", err)
				}
			}))
			defer srv.Close()

			client := New(srv.URL)
			if err := client.SetVADThreshold(context.Background(), tt.threshold); err != nil {
				t.Fatalf("SetVADThreshold() error: %v", err)
			}
		})
	}
}

func TestSetMicMute(t *testing.T) {
	tests := []struct {
		name  string
		muted bool
	}{
		{"mute", true},
		{"unmute", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/mic/mute" {
					t.Errorf("path = %s, want /mic/mute", r.URL.Path)
				}

				var got map[string]bool
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got["muted"] != tt.muted {
					t.Errorf("muted = %v, want %v", got["muted"], tt.muted)
				}

				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "muted": got["muted"]}); err != nil {
					t.Fatalf("encode: %v", err)
				}
			}))
			defer srv.Close()

			client := New(srv.URL)
			if err := client.SetMicMute(context.Background(), tt.muted); err != nil {
				t.Fatalf("SetMicMute() error: %v", err)
			}
		})
	}
}

func TestSetGain(t *testing.T) {
	tests := []struct {
		name string
		gain float64
	}{
		{"unity", 1.0},
		{"boost", 2.0},
		{"reduce", 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/gain" {
					t.Errorf("path = %s, want /gain", r.URL.Path)
				}

				var got map[string]float64
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got["gain"] != tt.gain {
					t.Errorf("gain = %f, want %f", got["gain"], tt.gain)
				}

				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "gain": got["gain"]}); err != nil {
					t.Fatalf("encode: %v", err)
				}
			}))
			defer srv.Close()

			client := New(srv.URL)
			if err := client.SetGain(context.Background(), tt.gain); err != nil {
				t.Fatalf("SetGain() error: %v", err)
			}
		})
	}
}

func TestConfig(t *testing.T) {
	want := ConfigResponse{
		VADThreshold: 0.015,
		Muted:        false,
		Gain:         1.0,
		SpeachesURL:  "http://localhost:34331",
		PocketTTSURL: "http://localhost:49112",
		STTURL:       "http://localhost:34331",
		Port:         5111,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/config" {
			t.Errorf("path = %s, want /config", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(want); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}))
	defer srv.Close()

	client := New(srv.URL)
	got, err := client.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if got.VADThreshold != want.VADThreshold {
		t.Errorf("vad_threshold = %f, want %f", got.VADThreshold, want.VADThreshold)
	}
	if got.Muted != want.Muted {
		t.Errorf("muted = %v, want %v", got.Muted, want.Muted)
	}
	if got.Gain != want.Gain {
		t.Errorf("gain = %f, want %f", got.Gain, want.Gain)
	}
	if got.Port != want.Port {
		t.Errorf("port = %d, want %d", got.Port, want.Port)
	}
}

func TestTranscriptStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/transcripts/stream" {
			t.Errorf("path = %s, want /transcripts/stream", r.URL.Path)
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("flusher not supported")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Send two events
		if _, err := fmt.Fprintf(w, "data: hello world\n\n"); err != nil {
			return
		}
		flusher.Flush()
		if _, err := fmt.Fprintf(w, "data: second utterance\n\n"); err != nil {
			return
		}
		flusher.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := New(srv.URL)
	ch, err := client.TranscriptStream(ctx)
	if err != nil {
		t.Fatalf("TranscriptStream() error: %v", err)
	}

	got1 := <-ch
	if got1 != "hello world" {
		t.Errorf("transcript 1 = %q, want %q", got1, "hello world")
	}

	got2 := <-ch
	if got2 != "second utterance" {
		t.Errorf("transcript 2 = %q, want %q", got2, "second utterance")
	}
}
