package audio

import (
	"fmt"
	"sync"

	"github.com/gordonklaus/portaudio"
)

// MicStream captures audio from the default input device at 48kHz mono float32.
type MicStream struct {
	stream *portaudio.Stream
	frames chan []float32
	buf    []float32
	logf   func(string, ...any)
	mu     sync.Mutex
	closed bool
}

// MicConfig holds configuration for the microphone stream.
type MicConfig struct {
	FrameSize  int
	SampleRate float64
	ChanSize   int
	Logf       func(string, ...any)
}

// DefaultMicConfig returns a MicConfig with standard defaults.
func DefaultMicConfig() MicConfig {
	return MicConfig{
		FrameSize:  FrameSize,
		SampleRate: SampleRate,
		ChanSize:   100,
	}
}

// NewMicStream creates a new microphone capture stream.
func NewMicStream(cfg MicConfig) (*MicStream, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	m := &MicStream{
		frames: make(chan []float32, cfg.ChanSize),
		buf:    make([]float32, cfg.FrameSize),
		logf:   logf,
	}

	stream, err := portaudio.OpenDefaultStream(1, 0, cfg.SampleRate, cfg.FrameSize, m.callback)
	if err != nil {
		return nil, fmt.Errorf("mic: open stream: %w", err)
	}
	m.stream = stream

	logf("mic: stream opened (rate=%.0f, frame=%d)", cfg.SampleRate, cfg.FrameSize)
	return m, nil
}

// callback is the portaudio callback. It copies input data and sends to the channel
// without blocking (drops frames if channel is full).
func (m *MicStream) callback(in []float32) {
	frame := make([]float32, len(in))
	copy(frame, in)

	select {
	case m.frames <- frame:
	default:
		// Drop frame rather than block the audio thread
	}
}

// Start begins audio capture.
func (m *MicStream) Start() error {
	if err := m.stream.Start(); err != nil {
		return fmt.Errorf("mic: start stream: %w", err)
	}
	m.logf("mic: capture started")
	return nil
}

// Stop halts audio capture and closes the channel.
func (m *MicStream) Stop() error {
	if err := m.stream.Stop(); err != nil {
		return fmt.Errorf("mic: stop stream: %w", err)
	}
	if err := m.stream.Close(); err != nil {
		return fmt.Errorf("mic: close stream: %w", err)
	}

	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.frames)
	}
	m.mu.Unlock()

	m.logf("mic: capture stopped")
	return nil
}

// Frames returns the channel of captured audio frames.
func (m *MicStream) Frames() <-chan []float32 {
	return m.frames
}
