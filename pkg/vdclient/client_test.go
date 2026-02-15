package vdclient

import (
	"context"
	"encoding/json"
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
