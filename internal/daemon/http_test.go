package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/realnikolaj/voicedaemon/internal/tts"
)

// mockPipeline implements AudioPipeline for testing.
type mockPipeline struct {
	mu        sync.Mutex
	threshold float64
	muted     bool
	gain      float64
}

func newMockPipeline() *mockPipeline {
	return &mockPipeline{threshold: 0.015, gain: 1.0}
}

func (m *mockPipeline) SetVADThreshold(t float64) { m.mu.Lock(); m.threshold = t; m.mu.Unlock() }
func (m *mockPipeline) VADThreshold() float64     { m.mu.Lock(); defer m.mu.Unlock(); return m.threshold }
func (m *mockPipeline) SetMuted(v bool)           { m.mu.Lock(); m.muted = v; m.mu.Unlock() }
func (m *mockPipeline) Muted() bool               { m.mu.Lock(); defer m.mu.Unlock(); return m.muted }
func (m *mockPipeline) SetGain(g float64)         { m.mu.Lock(); m.gain = g; m.mu.Unlock() }
func (m *mockPipeline) Gain() float64             { m.mu.Lock(); defer m.mu.Unlock(); return m.gain }

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

func startTestHTTPServer(t *testing.T, q TTSQueue) (*HTTPServer, *mockPipeline) {
	t.Helper()
	cfg := DefaultHTTPConfig()
	cfg.Port = 0 // random port
	p := newMockPipeline()
	srv := NewHTTPServer(cfg, q, p)

	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return srv, p
}

func TestHTTPHealth(t *testing.T) {
	q := &mockQueue{}
	srv, _ := startTestHTTPServer(t, q)

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
	for _, field := range []string{"queue_depth", "speaches_url", "pocket_tts_url", "stt_socket"} {
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
			srv, _ := startTestHTTPServer(t, q)

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
			srv, _ := startTestHTTPServer(t, q)

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
	srv, _ := startTestHTTPServer(t, q)

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
	srv, _ := startTestHTTPServer(t, q)
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

func TestHTTPSetVADThreshold(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantThresh float64
	}{
		{"valid threshold", `{"threshold": 0.03}`, http.StatusOK, 0.03},
		{"zero threshold", `{"threshold": 0}`, http.StatusBadRequest, 0.015},
		{"negative threshold", `{"threshold": -0.5}`, http.StatusBadRequest, 0.015},
		{"invalid json", `not json`, http.StatusBadRequest, 0.015},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &mockQueue{}
			srv, p := startTestHTTPServer(t, q)

			resp, err := http.Post(
				"http://"+srv.Addr()+"/vad/threshold",
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

			p.mu.Lock()
			got := p.threshold
			p.mu.Unlock()
			if got != tt.wantThresh {
				t.Errorf("threshold = %f, want %f", got, tt.wantThresh)
			}
		})
	}
}

func TestHTTPSetMicMute(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantMuted  bool
	}{
		{"mute", `{"muted": true}`, http.StatusOK, true},
		{"unmute", `{"muted": false}`, http.StatusOK, false},
		{"invalid json", `not json`, http.StatusBadRequest, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &mockQueue{}
			srv, p := startTestHTTPServer(t, q)

			resp, err := http.Post(
				"http://"+srv.Addr()+"/mic/mute",
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

			p.mu.Lock()
			got := p.muted
			p.mu.Unlock()
			if got != tt.wantMuted {
				t.Errorf("muted = %v, want %v", got, tt.wantMuted)
			}
		})
	}
}

func TestHTTPSetGain(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantGain   float64
	}{
		{"valid gain", `{"gain": 1.5}`, http.StatusOK, 1.5},
		{"unity gain", `{"gain": 1.0}`, http.StatusOK, 1.0},
		{"zero gain", `{"gain": 0}`, http.StatusBadRequest, 1.0},
		{"negative gain", `{"gain": -1}`, http.StatusBadRequest, 1.0},
		{"invalid json", `not json`, http.StatusBadRequest, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &mockQueue{}
			srv, p := startTestHTTPServer(t, q)

			resp, err := http.Post(
				"http://"+srv.Addr()+"/gain",
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

			p.mu.Lock()
			got := p.gain
			p.mu.Unlock()
			if got != tt.wantGain {
				t.Errorf("gain = %f, want %f", got, tt.wantGain)
			}
		})
	}
}

func TestHTTPConfig(t *testing.T) {
	q := &mockQueue{}
	srv, p := startTestHTTPServer(t, q)

	// Set some non-default values
	p.mu.Lock()
	p.threshold = 0.05
	p.muted = true
	p.gain = 2.0
	p.mu.Unlock()

	resp, err := http.Get("http://" + srv.Addr() + "/config")
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

	checks := map[string]any{
		"vad_threshold":  0.05,
		"muted":          true,
		"gain":           2.0,
		"speaches_url":   "http://localhost:34331",
		"pocket_tts_url": "http://localhost:49112",
		"port":           float64(0), // test uses port 0 for random assignment
	}
	for k, want := range checks {
		if body[k] != want {
			t.Errorf("%s = %v, want %v", k, body[k], want)
		}
	}
}

func TestHTTPTranscriptStream(t *testing.T) {
	q := &mockQueue{}
	srv, _ := startTestHTTPServer(t, q)
	base := "http://" + srv.Addr()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/transcripts/stream", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	// Read events in a goroutine
	events := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				events <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	// Give the SSE goroutine time to start reading
	time.Sleep(50 * time.Millisecond)

	// Broadcast two transcripts
	srv.BroadcastTranscript("hello world")
	srv.BroadcastTranscript("second utterance")

	// Read them back
	select {
	case got := <-events:
		if got != "hello world" {
			t.Errorf("event 1 = %q, want %q", got, "hello world")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event 1")
	}

	select {
	case got := <-events:
		if got != "second utterance" {
			t.Errorf("event 2 = %q, want %q", got, "second utterance")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event 2")
	}
}
