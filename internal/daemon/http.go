package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/realnikolaj/voicedaemon/internal/tts"
)

// HTTPConfig holds configuration for the HTTP server.
type HTTPConfig struct {
	Port         int
	SpeachesURL  string
	PocketTTSURL string
	STTURL       string
	SocketPath   string
	Logf         func(string, ...any)
}

// DefaultHTTPConfig returns HTTP config with standard defaults.
func DefaultHTTPConfig() HTTPConfig {
	return HTTPConfig{
		Port:         5111,
		SpeachesURL:  "http://localhost:34331",
		PocketTTSURL: "http://localhost:49112",
		STTURL:       "http://localhost:34331",
		SocketPath:   "/tmp/voice-daemon.sock",
	}
}

// TTSQueue is the interface the HTTP server uses to enqueue TTS jobs.
type TTSQueue interface {
	Enqueue(job tts.Job)
	Depth() int
	StopPlayback()
}

// HTTPServer serves the TTS HTTP API.
type HTTPServer struct {
	cfg      HTTPConfig
	logf     func(string, ...any)
	queue    TTSQueue
	server   *http.Server
	listener net.Listener
}

// NewHTTPServer creates a new HTTP server.
func NewHTTPServer(cfg HTTPConfig, queue TTSQueue) *HTTPServer {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &HTTPServer{
		cfg:   cfg,
		logf:  logf,
		queue: queue,
	}
}

// speakRequest is the JSON body for POST /speak.
type speakRequest struct {
	Text    string `json:"text"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	Voice   string `json:"voice,omitempty"`
	NoLog   bool   `json:"nolog,omitempty"`
}

// Start begins serving HTTP on the configured port.
func (h *HTTPServer) Start(_ context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /speak", h.handleSpeak)
	mux.HandleFunc("POST /stop", h.handleStop)
	mux.HandleFunc("GET /health", h.handleHealth)

	addr := fmt.Sprintf(":%d", h.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http: listen: %w", err)
	}
	h.listener = listener

	h.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		h.logf("http: serving on %s", addr)
		if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			h.logf("http: serve error: %v", err)
		}
	}()

	return nil
}

func (h *HTTPServer) handleSpeak(w http.ResponseWriter, r *http.Request) {
	var req speakRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if req.Text == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	backend := tts.BackendSpeaches
	if req.Backend == "pocket" || req.Backend == "pockettts" {
		backend = tts.BackendPocket
	}

	var opts *tts.StreamOpts
	if req.Model != "" || req.Voice != "" {
		opts = &tts.StreamOpts{
			Model: req.Model,
			Voice: req.Voice,
		}
	}

	h.queue.Enqueue(tts.Job{
		Text:    req.Text,
		Backend: backend,
		Opts:    opts,
		NoLog:   req.NoLog,
	})

	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":      "queued",
		"queue_depth": h.queue.Depth(),
		"backend":     string(backend),
	})
}

func (h *HTTPServer) handleStop(w http.ResponseWriter, _ *http.Request) {
	h.queue.StopPlayback()

	h.writeJSON(w, http.StatusOK, map[string]string{
		"status": "stopped",
	})
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"queue_depth":    h.queue.Depth(),
		"speaches_url":   h.cfg.SpeachesURL,
		"pocket_tts_url": h.cfg.PocketTTSURL,
		"stt_url":        h.cfg.STTURL,
		"stt_socket":     h.cfg.SocketPath,
	})
}

func (h *HTTPServer) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logf("http: encode response: %v", err)
	}
}

// Addr returns the listener address (useful for tests with port 0).
func (h *HTTPServer) Addr() string {
	if h.listener != nil {
		return h.listener.Addr().String()
	}
	return ""
}

// Close gracefully shuts down the HTTP server.
func (h *HTTPServer) Close() error {
	if h.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("http: shutdown: %w", err)
	}
	h.logf("http: closed")
	return nil
}
