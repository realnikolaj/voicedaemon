package tts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockSpeaker implements the Speaker interface for testing.
type mockSpeaker struct {
	mu         sync.Mutex
	began      int
	ended      int
	stopped    int
	fedBytes   int
	waitReturn error
}

func (m *mockSpeaker) BeginUtterance() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.began++
}

func (m *mockSpeaker) Feed(data []byte, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fedBytes += len(data)
}

func (m *mockSpeaker) EndUtterance() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ended++
}

func (m *mockSpeaker) WaitUtterance(_ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.waitReturn
}

func (m *mockSpeaker) StopUtterance() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped++
}

func newTestServer(t *testing.T, response []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(response); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
}

func TestQueueBasic(t *testing.T) {
	pcmData := make([]byte, 2400)
	srv := newTestServer(t, pcmData)
	defer srv.Close()

	spk := &mockSpeaker{}
	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	q := NewQueue(QueueConfig{
		Client:  client,
		Speaker: spk,
	})
	q.Start()

	q.Enqueue(Job{
		Text:    "hello",
		Backend: BackendSpeaches,
	})

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	q.Stop()

	spk.mu.Lock()
	defer spk.mu.Unlock()

	if spk.began < 1 {
		t.Errorf("BeginUtterance called %d times, want >= 1", spk.began)
	}
	if spk.ended < 1 {
		t.Errorf("EndUtterance called %d times, want >= 1", spk.ended)
	}
	if spk.fedBytes == 0 {
		t.Error("no data fed to speaker")
	}
}

func TestQueueLatestWins(t *testing.T) {
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		var req ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		// Slow response to simulate in-flight TTS
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(make([]byte, 100)); err != nil {
			// Client may have disconnected due to cancellation
			return
		}
	}))
	defer srv.Close()

	spk := &mockSpeaker{}
	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	q := NewQueue(QueueConfig{
		Client:  client,
		Speaker: spk,
	})
	q.Start()

	// Enqueue first job
	q.Enqueue(Job{Text: "first", Backend: BackendSpeaches})
	time.Sleep(10 * time.Millisecond)

	// Enqueue second job (should drain first)
	q.Enqueue(Job{Text: "second", Backend: BackendSpeaches})

	time.Sleep(400 * time.Millisecond)
	q.Stop()

	// The speaker should have been stopped at least once (abort of first job)
	spk.mu.Lock()
	defer spk.mu.Unlock()
	if spk.stopped < 1 {
		t.Errorf("StopUtterance called %d times, want >= 1", spk.stopped)
	}
}

func TestQueueStopPlayback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(make([]byte, 100)); err != nil {
			return
		}
	}))
	defer srv.Close()

	spk := &mockSpeaker{}
	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	q := NewQueue(QueueConfig{
		Client:  client,
		Speaker: spk,
	})
	q.Start()

	q.Enqueue(Job{Text: "hello", Backend: BackendSpeaches})
	time.Sleep(10 * time.Millisecond)

	q.StopPlayback()

	spk.mu.Lock()
	stopped := spk.stopped
	spk.mu.Unlock()

	if stopped < 1 {
		t.Errorf("StopUtterance called %d times, want >= 1", stopped)
	}

	q.Stop()
}

func TestQueueStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte("error")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer srv.Close()

	spk := &mockSpeaker{}
	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	q := NewQueue(QueueConfig{
		Client:  client,
		Speaker: spk,
	})
	q.Start()

	q.Enqueue(Job{Text: "hello", Backend: BackendSpeaches})
	time.Sleep(200 * time.Millisecond)

	q.Stop()

	// Should not have begun utterance on error
	spk.mu.Lock()
	defer spk.mu.Unlock()
	if spk.began > 0 {
		t.Errorf("BeginUtterance should not be called on stream error, got %d", spk.began)
	}
}

func TestQueueCancelContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	spk := &mockSpeaker{}
	cfg := DefaultClientConfig()
	cfg.SpeachesURL = srv.URL
	client := NewClient(cfg)

	q := NewQueue(QueueConfig{
		Client:  client,
		Speaker: spk,
	})
	q.Start()

	q.Enqueue(Job{Text: "slow", Backend: BackendSpeaches})
	time.Sleep(10 * time.Millisecond)

	// Stop should return quickly even with in-flight request
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		q.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-ctx.Done():
		t.Error("queue.Stop() did not return within timeout")
	}
}
