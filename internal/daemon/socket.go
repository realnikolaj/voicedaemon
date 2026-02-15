package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

// AudioPipeline is the interface the socket server uses to control the audio pipeline.
type AudioPipeline interface {
	StartListening()
	StopListening()
	VADState() VadStateString
}

// VadStateString is a type that has a String() method for VAD state.
type VadStateString interface {
	String() string
}

// Transcriber transcribes audio and returns text.
type Transcriber interface {
	TranscribeCallback() func(audio []float32)
}

// SocketConfig holds configuration for the Unix socket server.
type SocketConfig struct {
	Path string
	Logf func(string, ...any)
}

// DefaultSocketConfig returns socket config with standard defaults.
func DefaultSocketConfig() SocketConfig {
	return SocketConfig{
		Path: "/tmp/voice-daemon.sock",
	}
}

// SocketServer handles the Hammerspoon-compatible Unix socket protocol.
type SocketServer struct {
	cfg      SocketConfig
	logf     func(string, ...any)
	listener net.Listener

	mu          sync.Mutex
	transcripts []string
	activeConn  net.Conn
	sessions    map[net.Conn]bool

	// Callbacks set by the daemon
	onStart  func()
	onStop   func() string
	onCancel func()
	onStatus func() string
}

// NewSocketServer creates a new Unix socket server.
func NewSocketServer(cfg SocketConfig) *SocketServer {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &SocketServer{
		cfg:      cfg,
		logf:     logf,
		sessions: make(map[net.Conn]bool),
	}
}

// SetCallbacks sets the command handlers. Must be called before Start.
func (s *SocketServer) SetCallbacks(onStart func(), onStop func() string, onCancel func(), onStatus func() string) {
	s.onStart = onStart
	s.onStop = onStop
	s.onCancel = onCancel
	s.onStatus = onStatus
}

// Start begins listening on the Unix socket.
func (s *SocketServer) Start(ctx context.Context) error {
	// Clean up stale socket
	if err := os.Remove(s.cfg.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("socket: remove stale: %w", err)
	}

	listener, err := net.Listen("unix", s.cfg.Path)
	if err != nil {
		return fmt.Errorf("socket: listen: %w", err)
	}
	s.listener = listener

	// chmod 0666 for accessibility
	if err := os.Chmod(s.cfg.Path, 0666); err != nil {
		return fmt.Errorf("socket: chmod: %w", err)
	}

	s.logf("socket: listening on %s", s.cfg.Path)

	go s.acceptLoop(ctx)
	return nil
}

func (s *SocketServer) acceptLoop(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.logf("socket: accept error: %v", err)
				continue
			}
		}

		go s.handleConn(ctx, conn)
	}
}

func (s *SocketServer) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			s.logf("socket: conn close error: %v", err)
		}
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cmd := strings.TrimSpace(scanner.Text())
		s.logf("socket: received command: %q", cmd)

		switch cmd {
		case "start":
			s.handleStart(conn)
		case "stop":
			s.handleStop(conn)
		case "cancel":
			s.handleCancel(conn)
		case "status":
			s.handleStatus(conn)
		default:
			s.writeConn(conn, "error: unknown command\n")
		}
	}

	if err := scanner.Err(); err != nil {
		s.logf("socket: scanner error: %v", err)
	}

	// Clean up session
	s.mu.Lock()
	delete(s.sessions, conn)
	if s.activeConn == conn {
		s.activeConn = nil
	}
	s.mu.Unlock()
}

func (s *SocketServer) handleStart(conn net.Conn) {
	s.mu.Lock()
	s.activeConn = conn
	s.sessions[conn] = true
	s.transcripts = nil
	s.mu.Unlock()

	if s.onStart != nil {
		s.onStart()
	}

	s.writeConn(conn, "started\n")
}

func (s *SocketServer) handleStop(conn net.Conn) {
	var result string
	if s.onStop != nil {
		result = s.onStop()
	}

	s.mu.Lock()
	if result == "" {
		result = strings.Join(s.transcripts, " ")
	}
	s.transcripts = nil
	s.activeConn = nil
	delete(s.sessions, conn)
	s.mu.Unlock()

	if result == "" {
		result = ""
	}
	s.writeConn(conn, result+"\n")
}

func (s *SocketServer) handleCancel(conn net.Conn) {
	s.mu.Lock()
	s.transcripts = nil
	s.activeConn = nil
	delete(s.sessions, conn)
	s.mu.Unlock()

	if s.onCancel != nil {
		s.onCancel()
	}

	s.writeConn(conn, "cancelled\n")
}

func (s *SocketServer) handleStatus(conn net.Conn) {
	status := "idle"
	if s.onStatus != nil {
		status = s.onStatus()
	}
	s.writeConn(conn, status+"\n")
}

// PushTranscript sends a transcript to the active session connection
// and accumulates it for the stop command.
func (s *SocketServer) PushTranscript(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.transcripts = append(s.transcripts, text)

	if s.activeConn != nil {
		s.writeConn(s.activeConn, "transcript:"+text+"\n")
	}
}

func (s *SocketServer) writeConn(conn net.Conn, msg string) {
	if _, err := conn.Write([]byte(msg)); err != nil {
		s.logf("socket: write error: %v", err)
	}
}

// Close shuts down the socket server and removes the socket file.
func (s *SocketServer) Close() error {
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return fmt.Errorf("socket: close listener: %w", err)
		}
	}

	// Clean up socket file
	if err := os.Remove(s.cfg.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("socket: remove: %w", err)
	}

	s.logf("socket: closed")
	return nil
}
