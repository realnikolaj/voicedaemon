package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/realnikolaj/voicedaemon/internal/tts"
)

// mockQueue implements TTSQueue for testing.
type mockQueue struct {
	mu        sync.Mutex
	jobs      []tts.Job
	depth     int
	stopCalls int
}

func (m *mockQueue) Enqueue(job tts.Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append(m.jobs, job)
	m.depth++
}

func (m *mockQueue) Depth() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.depth
}

func (m *mockQueue) StopPlayback() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls++
	m.depth = 0
}

func startTestHTTPServer(t *testing.T, q TTSQueue) *HTTPServer {
	t.Helper()
	cfg := DefaultHTTPConfig()
	cfg.Port = 0 // random port
	srv := NewHTTPServer(cfg, q)

	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return srv
}

func TestHTTPHealth(t *testing.T) {
	q := &mockQueue{}
	srv := startTestHTTPServer(t, q)

	resp, err := http.Get("http://" + srv.Addr() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}

	// Verify all expected fields
	for _, field := range []string{"queue_depth", "speaches_url", "pocket_tts_url", "stt_url", "stt_socket"} {
		if _, ok := body[field]; !ok {
			t.Errorf("missing field %q in health response", field)
		}
	}
}

func TestHTTPSpeak(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantQueued bool
		wantNoLog  bool
	}{
		{
			name:       "basic speak",
			body:       `{"text": "hello world"}`,
			wantStatus: http.StatusOK,
			wantQueued: true,
		},
		{
			name:       "speak with backend",
			body:       `{"text": "hello", "backend": "pocket"}`,
			wantStatus: http.StatusOK,
			wantQueued: true,
		},
		{
			name:       "speak with model and voice",
			body:       `{"text": "hello", "model": "custom", "voice": "alice"}`,
			wantStatus: http.StatusOK,
			wantQueued: true,
		},
		{
			name:       "empty text",
			body:       `{"text": ""}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid json",
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "speak with nolog",
			body:       `{"text": "secret", "nolog": true}`,
			wantStatus: http.StatusOK,
			wantQueued: true,
			wantNoLog:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &mockQueue{}
			srv := startTestHTTPServer(t, q)

			resp, err := http.Post(
				"http://"+srv.Addr()+"/speak",
				"application/json",
				bytes.NewBufferString(tt.body),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			if tt.wantQueued {
				var body map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["status"] != "queued" {
					t.Errorf("status = %v, want queued", body["status"])
				}

				q.mu.Lock()
				defer q.mu.Unlock()
				if len(q.jobs) == 0 {
					t.Error("no jobs enqueued")
				}

				if tt.wantNoLog && len(q.jobs) > 0 && !q.jobs[0].NoLog {
					t.Error("job.NoLog = false, want true")
				}
			}
		})
	}
}

func TestHTTPSpeakBackendSelection(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		want    tts.Backend
	}{
		{"default speaches", "", tts.BackendSpeaches},
		{"explicit pocket", "pocket", tts.BackendPocket},
		{"pockettts alias", "pockettts", tts.BackendPocket},
		{"speaches", "speaches", tts.BackendSpeaches},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &mockQueue{}
			srv := startTestHTTPServer(t, q)

			body := map[string]string{"text": "hello"}
			if tt.backend != "" {
				body["backend"] = tt.backend
			}
			b, _ := json.Marshal(body)

			resp, err := http.Post(
				"http://"+srv.Addr()+"/speak",
				"application/json",
				bytes.NewReader(b),
			)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			q.mu.Lock()
			defer q.mu.Unlock()
			if len(q.jobs) != 1 {
				t.Fatalf("jobs = %d, want 1", len(q.jobs))
			}
			if q.jobs[0].Backend != tt.want {
				t.Errorf("backend = %v, want %v", q.jobs[0].Backend, tt.want)
			}
		})
	}
}

func TestHTTPStop(t *testing.T) {
	q := &mockQueue{}
	srv := startTestHTTPServer(t, q)

	resp, err := http.Post("http://"+srv.Addr()+"/stop", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "stopped" {
		t.Errorf("status = %v, want stopped", body["status"])
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopCalls != 1 {
		t.Errorf("StopPlayback called %d times, want 1", q.stopCalls)
	}
}

func TestHTTPMethodRouting(t *testing.T) {
	q := &mockQueue{}
	srv := startTestHTTPServer(t, q)
	base := "http://" + srv.Addr()

	// GET /speak should 405
	resp, err := http.Get(base + "/speak")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /speak = %d, want 405", resp.StatusCode)
	}

	// POST /health should 405
	resp, err = http.Post(base+"/health", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /health = %d, want 405", resp.StatusCode)
	}
}
