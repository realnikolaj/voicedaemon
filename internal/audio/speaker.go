package audio

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gordonklaus/portaudio"
)

const (
	speakerChunkQueueCap = 2000

	// preBufferFrames is the number of frames to accumulate before starting
	// playback. This absorbs network jitter from HTTP chunked TTS responses.
	// 20 frames × 10ms/frame = 200ms of audio.
	preBufferFrames = 20
)

// Speaker is a persistent portaudio output stream at OutputSampleRate mono int16.
// It stays open between utterances to avoid open/close latency.
type Speaker struct {
	stream     *portaudio.Stream
	queue      chan []int16
	logf       func(string, ...any)
	sampleRate float64
	frameSize  int

	mu       sync.Mutex
	playing  bool
	eos      bool
	done     chan struct{}
	aborted  bool
	gateOpen atomic.Bool
	residual []int16 // leftover samples from last Feed() that didn't fill a frame
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
		FrameSize:  OutputFrameSize,
		SampleRate: OutputSampleRate,
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
// and pads with silence when empty. Pre-buffer gate prevents playback until
// enough data has accumulated to absorb network jitter.
func (s *Speaker) callback(out []int16) {
	// Pre-buffer gate: output silence until enough data is buffered.
	if !s.gateOpen.Load() {
		for i := range out {
			out[i] = 0
		}
		return
	}

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
// The stream is reopened to re-acquire the current default output device,
// then the pre-buffer gate is closed; playback won't start until Feed()
// accumulates enough data or EndUtterance() forces it open.
func (s *Speaker) BeginUtterance() {
	if err := s.reopenStream(); err != nil {
		s.logf("speaker: reopen failed, continuing with existing stream: %v", err)
	}

	s.gateOpen.Store(false)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.playing = true
	s.eos = false
	s.aborted = false
	s.residual = nil
	// Drain any leftover chunks
	for len(s.queue) > 0 {
		<-s.queue
	}
	// Reset done channel
	select {
	case <-s.done:
	default:
	}

	s.logf("speaker: utterance started (pre-buffering)")
}

// Feed resamples PCM s16le data from srcRate to OutputSampleRate and enqueues chunks.
// When srcRate matches the output rate, resampling is skipped entirely.
// Residual samples from the previous call are prepended so frames stay aligned
// across HTTP chunk boundaries.
func (s *Speaker) Feed(data []byte, srcRate int) {
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	var raw []byte
	if srcRate == int(s.sampleRate) {
		raw = data
	} else {
		raw = ResampleS16LE(data, srcRate, int(s.sampleRate))
	}

	numSamples := len(raw) / 2
	samples := make([]int16, numSamples)
	for i := range numSamples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2 : i*2+2]))
	}

	// Prepend residual samples from previous Feed() call.
	if len(s.residual) > 0 {
		samples = append(s.residual, samples...)
		s.residual = nil
	}

	// Break into frame-sized chunks and enqueue
	for offset := 0; offset < len(samples); offset += s.frameSize {
		remaining := len(samples) - offset
		if remaining < s.frameSize {
			// Save incomplete frame for next Feed() call.
			s.residual = make([]int16, remaining)
			copy(s.residual, samples[offset:])
			break
		}
		chunk := make([]int16, s.frameSize)
		copy(chunk, samples[offset:offset+s.frameSize])

		select {
		case s.queue <- chunk:
		case <-time.After(500 * time.Millisecond):
			s.logf("speaker: queue full, dropped frame")
		}
	}

	// Open the pre-buffer gate once enough frames are queued.
	if !s.gateOpen.Load() && len(s.queue) >= preBufferFrames {
		s.gateOpen.Store(true)
		s.logf("speaker: pre-buffer gate opened (%d frames)", len(s.queue))
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
// Flushes any residual samples as a zero-padded final frame, then forces
// the pre-buffer gate open so short utterances still play.
func (s *Speaker) EndUtterance() {
	// Flush residual samples as a zero-padded final frame.
	if len(s.residual) > 0 {
		chunk := make([]int16, s.frameSize)
		copy(chunk, s.residual)
		s.residual = nil
		select {
		case s.queue <- chunk:
		default:
		}
	}

	// Force gate open — short utterances may not fill the pre-buffer.
	s.gateOpen.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.eos = true
	s.logf("speaker: utterance end signaled (queued=%d)", len(s.queue))

	// Done signal comes from the portaudio callback when it sees eos +
	// empty queue. Never signal done here — the callback is the only
	// consumer and knows when the last frame has actually been played.
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

// reopenStream stops and closes the current portaudio stream, then opens and
// starts a fresh default output stream. This re-acquires the current default
// output device so that device changes between utterances are picked up.
func (s *Speaker) reopenStream() error {
	if s.stream != nil {
		if err := s.stream.Stop(); err != nil {
			s.logf("speaker: reopen stop: %v", err)
		}
		if err := s.stream.Close(); err != nil {
			s.logf("speaker: reopen close: %v", err)
		}
		s.stream = nil
	}

	stream, err := portaudio.OpenDefaultStream(0, 1, s.sampleRate, s.frameSize, s.callback)
	if err != nil {
		return fmt.Errorf("speaker: reopen open stream: %w", err)
	}
	s.stream = stream

	if err := s.stream.Start(); err != nil {
		return fmt.Errorf("speaker: reopen start stream: %w", err)
	}

	s.logf("speaker: stream reopened (rate=%.0f, frame=%d)", s.sampleRate, s.frameSize)
	return nil
}

// StopStream stops and closes the current portaudio output stream, setting it to nil.
// Unlike Close(), this is non-terminal: reopenStream() (called by BeginUtterance)
// handles a nil stream and will create a fresh one for the next utterance.
func (s *Speaker) StopStream() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stream == nil {
		return
	}
	if err := s.stream.Stop(); err != nil {
		s.logf("speaker: idle stop: %v", err)
	}
	if err := s.stream.Close(); err != nil {
		s.logf("speaker: idle close: %v", err)
	}
	s.stream = nil
	s.logf("speaker: stream stopped (idle)")
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
