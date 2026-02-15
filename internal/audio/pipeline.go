package audio

import (
	"context"
	"fmt"
	"sync"
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

	proc, err := NewProcessor(logf)
	if err != nil {
		return nil, fmt.Errorf("pipeline: create processor: %w", err)
	}

	return &Pipeline{
		cfg:     cfg,
		logf:    logf,
		proc:    proc,
		speaker: speaker,
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

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.running = true

	p.wg.Add(1)
	go p.captureLoop(ctx)

	p.logf("pipeline: started")
	return nil
}

// captureLoop reads frames from mic, processes through APM, and feeds VAD.
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

// StartListening transitions VAD to listening state.
func (p *Pipeline) StartListening() {
	if p.vad != nil {
		p.vad.Start()
	}
}

// StopListening transitions VAD to idle state.
func (p *Pipeline) StopListening() {
	if p.vad != nil {
		p.vad.Stop()
	}
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
