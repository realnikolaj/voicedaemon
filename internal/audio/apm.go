//go:build !noapm

package audio

import (
	"fmt"

	apm "github.com/jfreymuth/go-webrtc-apm"
)

// Processor wraps go-webrtc-apm for noise suppression, AGC, VAD, and AEC.
type Processor struct {
	proc *apm.Processor
	logf func(string, ...any)
}

// NewProcessor creates an APM processor with OSOG-hardened defaults.
func NewProcessor(logf func(string, ...any)) (*Processor, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	proc, err := apm.New(apm.Config{
		NumChannels:      1,
		NoiseSuppression: &apm.NoiseSuppressionConfig{Enabled: true, Level: apm.NoiseSuppressionHigh},
		VoiceDetection:   &apm.VoiceDetectionConfig{Enabled: true, Likelihood: apm.VoiceDetectionModerate},
		EchoCancellation: &apm.EchoCancellationConfig{Enabled: true, SuppressionLevel: apm.SuppressionHigh, EnableDelayAgnostic: true},
		AutoGainControl:  &apm.AutoGainControlConfig{Enabled: true, Mode: apm.AdaptiveDigital},
		HighPassFilter:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("apm: new processor: %w", err)
	}
	logf("apm: processor created (NS=High, VAD=Moderate, AEC=High+DelayAgnostic, AGC=AdaptiveDigital)")
	return &Processor{proc: proc, logf: logf}, nil
}

// ProcessCapture runs the APM capture pipeline on a single 10ms frame (480 samples @ 48kHz).
// Returns the processed frame and whether voice activity was detected.
func (p *Processor) ProcessCapture(frame []float32) ([]float32, bool, error) {
	clean, hasVoice, err := p.proc.ProcessCapture(frame)
	if err != nil {
		return nil, false, fmt.Errorf("apm: process capture: %w", err)
	}
	return clean, hasVoice, nil
}

// ProcessRender feeds speaker audio into APM for echo cancellation reference.
func (p *Processor) ProcessRender(frame []float32) error {
	if err := p.proc.ProcessRender(frame); err != nil {
		return fmt.Errorf("apm: process render: %w", err)
	}
	return nil
}

// Close releases the APM processor resources.
// IMPORTANT: Must not be called while ProcessCapture/ProcessRender are in use.
// Follow shutdown order: cancel ctx → WaitGroup on goroutine → then Close.
func (p *Processor) Close() {
	p.proc.Close()
	p.logf("apm: processor closed")
}
