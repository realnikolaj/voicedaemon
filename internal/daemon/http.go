package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
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

// AudioPipeline is the interface the HTTP server uses for runtime audio control.
type AudioPipeline interface {
	SetVADThreshold(float64)
	VADThreshold() float64
	SetMuted(bool)
	Muted() bool
	SetGain(float64)
	Gain() float64
}

// HTTPServer serves the TTS HTTP API.
type HTTPServer struct {
	cfg      HTTPConfig
	logf     func(string, ...any)
	queue    TTSQueue
	pipeline AudioPipeline
	server   *http.Server
	listener net.Listener

	subsMu sync.Mutex
	subs   map[chan string]struct{}
}

// NewHTTPServer creates a new HTTP server.
func NewHTTPServer(cfg HTTPConfig, queue TTSQueue, pipeline AudioPipeline) *HTTPServer {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &HTTPServer{
		cfg:      cfg,
		logf:     logf,
		queue:    queue,
		pipeline: pipeline,
		subs:     make(map[chan string]struct{}),
	}
}

// speakRequest is the JSON body for POST /speak.
type speakRequest struct {
	Text    string `json:"text"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	Voice   string `json:"voice,omitempty"`
	NoLog   bool   `json:"nolog,omitempty"`
	Project string `json:"project,omitempty"`
}

// Start begins serving HTTP on the configured port.
func (h *HTTPServer) Start(_ context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /speak", h.handleSpeak)
	mux.HandleFunc("POST /stop", h.handleStop)
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("POST /vad/threshold", h.handleSetVADThreshold)
	mux.HandleFunc("POST /mic/mute", h.handleSetMicMute)
	mux.HandleFunc("POST /gain", h.handleSetGain)
	mux.HandleFunc("GET /config", h.handleConfig)
	mux.HandleFunc("GET /transcripts/stream", h.handleTranscriptStream)

	addr := fmt.Sprintf(":%d", h.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http: listen: %w", err)
	}
	h.listener = listener

	h.server = &http.Server{
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
		// No WriteTimeout — SSE connections are long-lived
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
		Project: req.Project,
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

func (h *HTTPServer) handleSetVADThreshold(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Threshold float64 `json:"threshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Threshold <= 0 {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "threshold must be positive"})
		return
	}
	h.pipeline.SetVADThreshold(req.Threshold)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"threshold": req.Threshold,
	})
}

func (h *HTTPServer) handleSetMicMute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Muted bool `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	h.pipeline.SetMuted(req.Muted)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"muted":  req.Muted,
	})
}

func (h *HTTPServer) handleSetGain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Gain float64 `json:"gain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Gain <= 0 {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gain must be positive"})
		return
	}
	h.pipeline.SetGain(req.Gain)
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"gain":   req.Gain,
	})
}

func (h *HTTPServer) handleConfig(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"vad_threshold":  h.pipeline.VADThreshold(),
		"muted":          h.pipeline.Muted(),
		"gain":           h.pipeline.Gain(),
		"speaches_url":   h.cfg.SpeachesURL,
		"pocket_tts_url": h.cfg.PocketTTSURL,
		"stt_url":        h.cfg.STTURL,
		"port":           h.cfg.Port,
	})
}

func (h *HTTPServer) handleTranscriptStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := make(chan string, 16)
	h.subsMu.Lock()
	h.subs[ch] = struct{}{}
	h.subsMu.Unlock()

	defer func() {
		h.subsMu.Lock()
		delete(h.subs, ch)
		h.subsMu.Unlock()
	}()

	h.logf("http: SSE subscriber connected")

	for {
		select {
		case <-r.Context().Done():
			h.logf("http: SSE subscriber disconnected")
			return
		case text := <-ch:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", text); err != nil {
				h.logf("http: SSE write error: %v", err)
				return
			}
			flusher.Flush()
		}
	}
}

// BroadcastTranscript sends a transcript to all SSE subscribers.
func (h *HTTPServer) BroadcastTranscript(text string) {
	h.subsMu.Lock()
	defer h.subsMu.Unlock()

	for ch := range h.subs {
		select {
		case ch <- text:
		default:
			h.logf("http: SSE subscriber slow, dropping transcript")
		}
	}
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
