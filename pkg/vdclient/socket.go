package vdclient

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
)

// STTSession is a Unix socket client for voicedaemon STT sessions.
type STTSession struct {
	conn    net.Conn
	scanner *bufio.Scanner

	mu          sync.Mutex
	transcripts chan string
	reading     bool
	stopResp    chan string
	closed      bool
}

// Dial connects to the voicedaemon Unix socket.
func Dial(socketPath string) (*STTSession, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("vdclient: dial %s: %w", socketPath, err)
	}

	return &STTSession{
		conn:    conn,
		scanner: bufio.NewScanner(conn),
	}, nil
}

// Start begins a recording session. Returns after "started" confirmation.
// Launches a background reader that routes transcript pushes to the
// Transcripts() channel.
func (s *STTSession) Start() error {
	s.mu.Lock()
	s.transcripts = make(chan string, 64)
	s.stopResp = make(chan string, 1)
	s.reading = true
	s.mu.Unlock()

	if err := s.send("start"); err != nil {
		return err
	}

	// Read the "started" confirmation before launching background reader.
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return fmt.Errorf("vdclient: read start response: %w", err)
		}
		return fmt.Errorf("vdclient: connection closed before start response")
	}

	line := s.scanner.Text()
	if line != "started" {
		return fmt.Errorf("vdclient: unexpected start response: %q", line)
	}

	go s.readLoop()
	return nil
}

// readLoop reads lines from the socket and routes them:
// - "transcript:..." lines go to the transcripts channel
// - Other lines go to stopResp (the stop/cancel response)
func (s *STTSession) readLoop() {
	defer func() {
		s.mu.Lock()
		if s.transcripts != nil {
			close(s.transcripts)
		}
		s.reading = false
		s.mu.Unlock()
	}()

	for s.scanner.Scan() {
		line := s.scanner.Text()
		if strings.HasPrefix(line, "transcript:") {
			text := strings.TrimPrefix(line, "transcript:")
			s.mu.Lock()
			ch := s.transcripts
			s.mu.Unlock()
			if ch != nil {
				select {
				case ch <- text:
				default:
				}
			}
		} else {
			// Stop/cancel response — deliver and exit loop
			s.stopResp <- line
			return
		}
	}
}

// Stop ends the session and returns the accumulated transcript.
func (s *STTSession) Stop() (string, error) {
	if err := s.send("stop"); err != nil {
		return "", err
	}

	resp, ok := <-s.stopResp
	if !ok {
		return "", fmt.Errorf("vdclient: connection closed before stop response")
	}
	return resp, nil
}

// Cancel discards the session.
func (s *STTSession) Cancel() error {
	if err := s.send("cancel"); err != nil {
		return err
	}

	resp, ok := <-s.stopResp
	if !ok {
		return fmt.Errorf("vdclient: connection closed before cancel response")
	}
	if resp != "cancelled" {
		return fmt.Errorf("vdclient: unexpected cancel response: %q", resp)
	}
	return nil
}

// Status returns current daemon state: "idle", "listening", "recording", "processing".
// Must be called outside of an active recording session (no Start() in progress).
func (s *STTSession) Status() (string, error) {
	if err := s.send("status"); err != nil {
		return "", err
	}

	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return "", fmt.Errorf("vdclient: read status response: %w", err)
		}
		return "", fmt.Errorf("vdclient: connection closed before status response")
	}

	return s.scanner.Text(), nil
}

// Transcripts returns a channel that receives pushed transcript fragments
// during a recording session. The channel is closed when Stop() or Cancel()
// completes.
func (s *STTSession) Transcripts() <-chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transcripts
}

// Close closes the underlying socket connection.
func (s *STTSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
}

func (s *STTSession) send(cmd string) error {
	_, err := s.conn.Write([]byte(cmd + "\n"))
	if err != nil {
		return fmt.Errorf("vdclient: send %q: %w", cmd, err)
	}
	return nil
}
