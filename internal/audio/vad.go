package audio

import (
	"sync"
)

// VadState represents the current state of the VAD state machine.
type VadState int

const (
	VadIdle VadState = iota
	VadListening
	VadRecording
	VadProcessing
)

// String returns a human-readable state name.
func (s VadState) String() string {
	switch s {
	case VadIdle:
		return "idle"
	case VadListening:
		return "listening"
	case VadRecording:
		return "recording"
	case VadProcessing:
		return "processing"
	default:
		return "unknown"
	}
}

const (
	// DefaultPreBufferFrames is ~320ms of pre-buffer (32 frames × 10ms at 48kHz).
	DefaultPreBufferFrames = 32

	// DefaultSilenceGapFrames is ~800ms silence gap (80 consecutive non-voice frames × 10ms).
	DefaultSilenceGapFrames = 80
)

// VADConfig holds configuration for the VAD state machine.
type VADConfig struct {
	PreBufferFrames  int
	SilenceGapFrames int
	Logf             func(string, ...any)
}

// DefaultVADConfig returns VAD config with standard defaults.
func DefaultVADConfig() VADConfig {
	return VADConfig{
		PreBufferFrames:  DefaultPreBufferFrames,
		SilenceGapFrames: DefaultSilenceGapFrames,
	}
}

// VADMachine implements a voice activity detection state machine with pre-buffering.
type VADMachine struct {
	cfg         VADConfig
	logf        func(string, ...any)
	onUtterance func(audio []float32)

	mu           sync.Mutex
	state        VadState
	preBuffer    [][]float32
	preBufIdx    int
	preBufFull   bool
	recorded     [][]float32
	silenceCount int
}

// NewVADMachine creates a new VAD state machine.
func NewVADMachine(cfg VADConfig, onUtterance func(audio []float32)) *VADMachine {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &VADMachine{
		cfg:         cfg,
		logf:        logf,
		onUtterance: onUtterance,
		state:       VadIdle,
		preBuffer:   make([][]float32, cfg.PreBufferFrames),
	}
}

// State returns the current VAD state.
func (v *VADMachine) State() VadState {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state
}

// Start transitions from IDLE to LISTENING.
func (v *VADMachine) Start() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.state = VadListening
	v.preBufIdx = 0
	v.preBufFull = false
	v.recorded = nil
	v.silenceCount = 0
	for i := range v.preBuffer {
		v.preBuffer[i] = nil
	}

	v.logf("vad: state → listening")
}

// Stop transitions to IDLE and discards any buffered audio.
func (v *VADMachine) Stop() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.state = VadIdle
	v.recorded = nil
	v.silenceCount = 0

	v.logf("vad: state → idle")
}

// ProcessFrame feeds a frame into the state machine.
// Called from the capture goroutine with the processed frame and voice detection result.
func (v *VADMachine) ProcessFrame(samples []float32, hasVoice bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Always update pre-buffer (circular)
	frameCopy := make([]float32, len(samples))
	copy(frameCopy, samples)

	switch v.state {
	case VadIdle:
		// Do nothing
		return

	case VadListening:
		if hasVoice {
			v.state = VadRecording
			v.silenceCount = 0
			// Drain pre-buffer before the voice frame overwrites it
			v.recorded = v.drainPreBuffer()
			v.recorded = append(v.recorded, frameCopy)
			v.logf("vad: state → recording (voice detected)")
		} else {
			// Update pre-buffer only with non-voice frames
			v.preBuffer[v.preBufIdx] = frameCopy
			v.preBufIdx++
			if v.preBufIdx >= v.cfg.PreBufferFrames {
				v.preBufIdx = 0
				v.preBufFull = true
			}
		}

	case VadRecording:
		v.recorded = append(v.recorded, frameCopy)

		if !hasVoice {
			v.silenceCount++
			if v.silenceCount >= v.cfg.SilenceGapFrames {
				v.state = VadProcessing
				v.logf("vad: state → processing (silence gap reached: %d frames)", v.silenceCount)

				// Concatenate all recorded frames
				audio := v.concatenateRecorded()
				v.recorded = nil
				v.silenceCount = 0

				// Transition to processing and call callback
				// Unlock before calling onUtterance to avoid holding lock during transcription
				v.mu.Unlock()
				if v.onUtterance != nil {
					v.onUtterance(audio)
				}
				v.mu.Lock()

				// After processing, return to listening
				v.state = VadListening
				v.preBufIdx = 0
				v.preBufFull = false
				for i := range v.preBuffer {
					v.preBuffer[i] = nil
				}
				v.logf("vad: state → listening (processing complete)")
			}
		} else {
			v.silenceCount = 0
		}

	case VadProcessing:
		// Should not normally receive frames during processing,
		// but if we do, just buffer them
	}
}

// drainPreBuffer returns the pre-buffer contents in order.
func (v *VADMachine) drainPreBuffer() [][]float32 {
	var frames [][]float32
	if v.preBufFull {
		// Start from current index (oldest) and wrap around
		for i := range v.cfg.PreBufferFrames {
			idx := (v.preBufIdx + i) % v.cfg.PreBufferFrames
			if v.preBuffer[idx] != nil {
				frames = append(frames, v.preBuffer[idx])
			}
		}
	} else {
		for i := range v.preBufIdx {
			if v.preBuffer[i] != nil {
				frames = append(frames, v.preBuffer[i])
			}
		}
	}
	return frames
}

// concatenateRecorded joins all recorded frames into a single audio slice.
func (v *VADMachine) concatenateRecorded() []float32 {
	totalLen := 0
	for _, f := range v.recorded {
		totalLen += len(f)
	}
	audio := make([]float32, 0, totalLen)
	for _, f := range v.recorded {
		audio = append(audio, f...)
	}
	return audio
}
