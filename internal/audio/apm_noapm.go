//go:build noapm && !silero

package audio

import (
	"math"
	"sync"
)

// defaultEnergyThreshold is the RMS level above which the energy-based VAD reports voice.
const defaultEnergyThreshold = 0.015

// Processor is a no-op stub when built without libwebrtc-audio-processing.
// Audio passes through unmodified. Voice detection uses energy-based RMS thresholding.
type Processor struct {
	logf      func(string, ...any)
	mu        sync.Mutex
	threshold float64
}

// NewProcessor creates a no-op processor stub with energy-based VAD.
func NewProcessor(cfg PipelineConfig) (*Processor, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf("apm: using no-op stub with energy VAD (built with -tags noapm)")
	return &Processor{logf: logf, threshold: defaultEnergyThreshold}, nil
}

// ProcessCapture returns the frame unmodified. Voice activity is detected
// via RMS energy thresholding as a fallback for the real APM VAD.
func (p *Processor) ProcessCapture(frame []float32) ([]float32, bool, error) {
	if len(frame) == 0 {
		return frame, false, nil
	}

	p.mu.Lock()
	thresh := p.threshold
	p.mu.Unlock()

	var sumSq float64
	for _, s := range frame {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(frame)))
	return frame, rms > thresh, nil
}

// SetVADThreshold updates the energy VAD threshold at runtime.
func (p *Processor) SetVADThreshold(threshold float64) {
	p.mu.Lock()
	p.threshold = threshold
	p.mu.Unlock()
	p.logf("apm: VAD threshold set to %f", threshold)
}

// VADThreshold returns the current energy VAD threshold.
func (p *Processor) VADThreshold() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.threshold
}

// ProcessRender is a no-op in the stub build.
func (p *Processor) ProcessRender(frame []float32) error {
	return nil
}

// Close is a no-op in the stub build.
func (p *Processor) Close() {
	p.logf("apm: no-op processor closed")
}
