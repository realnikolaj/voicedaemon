package audio

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/gordonklaus/portaudio"
)

const (
	speakerChunkQueueCap = 200
)

// Speaker is a persistent portaudio output stream at 48kHz mono int16.
// It stays open between utterances to avoid open/close latency.
type Speaker struct {
	stream     *portaudio.Stream
	queue      chan []int16
	logf       func(string, ...any)
	sampleRate float64
	frameSize  int

	mu      sync.Mutex
	playing bool
	eos     bool
	done    chan struct{}
	aborted bool
}

// SpeakerConfig holds configuration for the speaker output.
type SpeakerConfig struct {
	FrameSize  int
	SampleRate float64
	Logf       func(string, ...any)
}

// DefaultSpeakerConfig returns a SpeakerConfig with standard defaults.
func DefaultSpeakerConfig() SpeakerConfig {
	return SpeakerConfig{
		FrameSize:  FrameSize,
		SampleRate: SampleRate,
	}
}

// NewSpeaker creates a new persistent speaker output.
func NewSpeaker(cfg SpeakerConfig) *Speaker {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &Speaker{
		queue:      make(chan []int16, speakerChunkQueueCap),
		logf:       logf,
		sampleRate: cfg.SampleRate,
		frameSize:  cfg.FrameSize,
		done:       make(chan struct{}),
	}
}

// Open initializes the portaudio output stream.
func (s *Speaker) Open() error {
	stream, err := portaudio.OpenDefaultStream(0, 1, s.sampleRate, s.frameSize, s.callback)
	if err != nil {
		return fmt.Errorf("speaker: open stream: %w", err)
	}
	s.stream = stream

	if err := s.stream.Start(); err != nil {
		return fmt.Errorf("speaker: start stream: %w", err)
	}

	s.logf("speaker: stream opened (rate=%.0f, frame=%d)", s.sampleRate, s.frameSize)
	return nil
}

// callback is the portaudio output callback. It pulls chunks from the queue
// and pads with silence when empty.
func (s *Speaker) callback(out []int16) {
	select {
	case chunk := <-s.queue:
		// Hold through the copy — no lock release between data read and output
		n := copy(out, chunk)
		for i := n; i < len(out); i++ {
			out[i] = 0
		}
	default:
		for i := range out {
			out[i] = 0
		}
		// If EOS was signaled and queue is empty, playback is done
		s.mu.Lock()
		if s.eos && len(s.queue) == 0 && s.playing {
			s.playing = false
			select {
			case s.done <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
	}
}

// BeginUtterance resets state for a new utterance.
func (s *Speaker) BeginUtterance() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.playing = true
	s.eos = false
	s.aborted = false
	// Drain any leftover chunks
	for len(s.queue) > 0 {
		<-s.queue
	}
	// Reset done channel
	select {
	case <-s.done:
	default:
	}

	s.logf("speaker: utterance started")
}

// Feed resamples PCM s16le data from srcRate to 48kHz and enqueues chunks.
func (s *Speaker) Feed(data []byte, srcRate int) {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	resampled := ResampleS16LE(data, srcRate, int(s.sampleRate))

	numSamples := len(resampled) / 2
	samples := make([]int16, numSamples)
	for i := range numSamples {
		samples[i] = int16(binary.LittleEndian.Uint16(resampled[i*2 : i*2+2]))
	}

	// Break into frame-sized chunks and enqueue
	for offset := 0; offset < len(samples); offset += s.frameSize {
		end := offset + s.frameSize
		if end > len(samples) {
			end = len(samples)
		}
		chunk := make([]int16, s.frameSize)
		copy(chunk, samples[offset:end])

		select {
		case s.queue <- chunk:
		default:
			// Drop chunk if queue is full rather than block
		}
	}
}

// FeedFloat32 converts float32 samples to int16 and enqueues for playback.
// Used for AEC render path — samples are already at 48kHz.
func (s *Speaker) FeedFloat32(samples []float32) {
	chunk := make([]int16, len(samples))
	for i, v := range samples {
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		chunk[i] = int16(v * 32767)
	}

	select {
	case s.queue <- chunk:
	default:
	}
}

// EndUtterance signals that no more data will be fed for this utterance.
func (s *Speaker) EndUtterance() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eos = true
	s.logf("speaker: utterance end signaled")

	// If queue is already empty, signal done immediately
	if len(s.queue) == 0 {
		s.playing = false
		select {
		case s.done <- struct{}{}:
		default:
		}
	}
}

// WaitUtterance blocks until the current utterance finishes playback or timeout.
func (s *Speaker) WaitUtterance(timeout time.Duration) error {
	select {
	case <-s.done:
		s.logf("speaker: utterance playback complete")
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("speaker: utterance playback timeout after %v", timeout)
	}
}

// StopUtterance aborts the current utterance immediately.
func (s *Speaker) StopUtterance() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.aborted = true
	s.eos = true
	s.playing = false

	// Drain queue
	for len(s.queue) > 0 {
		<-s.queue
	}

	select {
	case s.done <- struct{}{}:
	default:
	}

	s.logf("speaker: utterance aborted")
}

// Close shuts down the portaudio output stream.
func (s *Speaker) Close() error {
	if s.stream == nil {
		return nil
	}
	if err := s.stream.Stop(); err != nil {
		return fmt.Errorf("speaker: stop stream: %w", err)
	}
	if err := s.stream.Close(); err != nil {
		return fmt.Errorf("speaker: close stream: %w", err)
	}
	s.logf("speaker: stream closed")
	return nil
}
