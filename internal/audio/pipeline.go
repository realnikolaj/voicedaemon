package audio

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// PipelineConfig holds configuration for the audio pipeline.
type PipelineConfig struct {
	MicConfig     MicConfig
	SpeakerConfig SpeakerConfig
	VADConfig     VADConfig
	Logf          func(string, ...any)
}

// DefaultPipelineConfig returns a PipelineConfig with standard defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		MicConfig:     DefaultMicConfig(),
		SpeakerConfig: DefaultSpeakerConfig(),
		VADConfig:     DefaultVADConfig(),
	}
}

// Pipeline orchestrates mic → APM → VAD, and speaker → APM render for AEC.
type Pipeline struct {
	cfg     PipelineConfig
	logf    func(string, ...any)
	proc    *Processor
	mic     *MicStream
	speaker *Speaker
	vad     *VADMachine

	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running bool

	muted  atomic.Bool
	gainMu sync.Mutex
	gain   float64

	sinkMu sync.Mutex
	sink   func([]float32) // optional audio sink (e.g. WebRTC track)
}

// NewPipeline creates a new audio pipeline.
func NewPipeline(cfg PipelineConfig, speaker *Speaker) (*Pipeline, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// Propagate logf to sub-configs
	cfg.MicConfig.Logf = logf
	cfg.SpeakerConfig.Logf = logf
	cfg.VADConfig.Logf = logf

	proc, err := NewProcessor(cfg)
	if err != nil {
		return nil, fmt.Errorf("pipeline: create processor: %w", err)
	}

	return &Pipeline{
		cfg:     cfg,
		logf:    logf,
		proc:    proc,
		speaker: speaker,
		gain:    1.0,
	}, nil
}

// Start begins the capture pipeline. The onUtterance callback is called
// when the VAD detects a complete utterance (voice followed by silence gap).
func (p *Pipeline) Start(onUtterance func(audio []float32)) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return fmt.Errorf("pipeline: already running")
	}

	p.vad = NewVADMachine(p.cfg.VADConfig, onUtterance)

	mic, err := NewMicStream(p.cfg.MicConfig)
	if err != nil {
		return fmt.Errorf("pipeline: create mic: %w", err)
	}
	p.mic = mic

	if err := p.mic.Start(); err != nil {
		return fmt.Errorf("pipeline: start mic: %w", err)
	}

	// Start paused — mic only opens when a session begins (StartListening).
	// This avoids the macOS orange mic indicator when idle.
	if err := p.mic.Pause(); err != nil {
		p.logf("pipeline: initial pause: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.running = true

	p.wg.Add(1)
	go p.captureLoop(ctx)

	p.logf("pipeline: started (mic paused until session)")
	return nil
}

// captureLoop reads frames from mic, processes through APM, feeds VAD,
// and optionally forwards raw audio to the WebRTC audio sink.
func (p *Pipeline) captureLoop(ctx context.Context) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-p.mic.Frames():
			if !ok {
				return
			}

			if p.muted.Load() {
				continue
			}

			p.gainMu.Lock()
			g := p.gain
			p.gainMu.Unlock()
			if g != 1.0 {
				for i := range frame {
					frame[i] *= float32(g)
				}
			}

			// Forward raw audio to the WebRTC sink (if set).
			// This runs before VAD gating so every frame reaches the server.
			p.sinkMu.Lock()
			sink := p.sink
			p.sinkMu.Unlock()
			if sink != nil {
				sink(frame)
			}

			// Skip local VAD processing when idle.
			vadState := p.vad.State()
			if vadState == VadIdle {
				continue
			}

			clean, hasVoice, err := p.proc.ProcessCapture(frame)
			if err != nil {
				p.logf("pipeline: process capture error: %v", err)
				continue
			}

			p.vad.ProcessFrame(clean, hasVoice)
		}
	}
}

// FeedRender feeds speaker audio into APM for echo cancellation reference.
// Must be called with audio at 48kHz before it goes to the speaker.
func (p *Pipeline) FeedRender(samples []float32) error {
	// Process in frame-sized chunks
	for offset := 0; offset < len(samples); offset += FrameSize {
		end := offset + FrameSize
		if end > len(samples) {
			end = len(samples)
		}
		frame := samples[offset:end]
		if len(frame) < FrameSize {
			// Pad short frame with zeros
			padded := make([]float32, FrameSize)
			copy(padded, frame)
			frame = padded
		}
		if err := p.proc.ProcessRender(frame); err != nil {
			return fmt.Errorf("pipeline: feed render: %w", err)
		}
	}
	return nil
}

// VADState returns the current VAD state.
func (p *Pipeline) VADState() VadState {
	if p.vad == nil {
		return VadIdle
	}
	return p.vad.State()
}

// StartListening transitions VAD to listening state and resumes the mic.
func (p *Pipeline) StartListening() {
	if err := p.ResumeMic(); err != nil {
		p.logf("pipeline: resume mic: %v", err)
	}
	if p.vad != nil {
		p.vad.Start()
	}
}

// StopListening transitions VAD to idle and pauses the mic.
// This releases the microphone so macOS hides the orange indicator.
func (p *Pipeline) StopListening() {
	if p.vad != nil {
		p.vad.Stop()
	}
	if err := p.PauseMic(); err != nil {
		p.logf("pipeline: pause mic: %v", err)
	}
}

// PauseMic pauses the portaudio mic stream without stopping the capture goroutine.
// When paused, no callbacks fire and the captureLoop blocks on the empty channel.
func (p *Pipeline) PauseMic() error {
	if p.mic == nil {
		return nil
	}
	return p.mic.Pause()
}

// ResumeMic resumes the portaudio mic stream after a PauseMic call.
func (p *Pipeline) ResumeMic() error {
	if p.mic == nil {
		return nil
	}
	return p.mic.Resume()
}

// SetMuted enables or disables mic muting. When muted, captured frames are discarded.
func (p *Pipeline) SetMuted(muted bool) {
	p.muted.Store(muted)
	p.logf("pipeline: muted = %v", muted)
}

// Muted returns whether the mic is currently muted.
func (p *Pipeline) Muted() bool {
	return p.muted.Load()
}

// SetGain sets the input gain multiplier applied to captured audio before VAD/STT.
func (p *Pipeline) SetGain(gain float64) {
	p.gainMu.Lock()
	p.gain = gain
	p.gainMu.Unlock()
	p.logf("pipeline: gain = %.2f", gain)
}

// Gain returns the current input gain multiplier.
func (p *Pipeline) Gain() float64 {
	p.gainMu.Lock()
	defer p.gainMu.Unlock()
	return p.gain
}

// SetAudioSink sets a callback that receives every captured audio frame.
// Used by the WebRTC client to stream audio to the Speaches realtime endpoint.
// Pass nil to remove the sink.
func (p *Pipeline) SetAudioSink(fn func([]float32)) {
	p.sinkMu.Lock()
	p.sink = fn
	p.sinkMu.Unlock()
}

// SetVADThreshold delegates to the audio processor's VAD threshold setter.
func (p *Pipeline) SetVADThreshold(threshold float64) {
	p.proc.SetVADThreshold(threshold)
}

// VADThreshold returns the current VAD threshold from the audio processor.
func (p *Pipeline) VADThreshold() float64 {
	return p.proc.VADThreshold()
}

// Stop shuts down the pipeline in the correct order:
// 1. Cancel context (signals capture goroutine to exit)
// 2. WaitGroup wait (ensure capture goroutine has exited)
// 3. Close APM (safe: no goroutine is using it)
func (p *Pipeline) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	p.logf("pipeline: stopping...")

	// Step 1: cancel context → capture goroutine will exit
	p.cancel()

	// Step 2: wait for capture goroutine to finish
	p.wg.Wait()

	// Step 3: stop mic stream
	if err := p.mic.Stop(); err != nil {
		p.logf("pipeline: mic stop error: %v", err)
	}

	// Step 4: close APM (safe: goroutine is done)
	p.proc.Close()

	p.running = false
	p.logf("pipeline: stopped")
	return nil
}
