//go:build noapm

package audio

import "math"

// energyThreshold is the RMS level above which the energy-based VAD reports voice.
const energyThreshold = 0.015

// Processor is a no-op stub when built without libwebrtc-audio-processing.
// Audio passes through unmodified. Voice detection uses energy-based RMS thresholding.
type Processor struct {
	logf func(string, ...any)
}

// NewProcessor creates a no-op processor stub with energy-based VAD.
func NewProcessor(logf func(string, ...any)) (*Processor, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf("apm: using no-op stub with energy VAD (built with -tags noapm)")
	return &Processor{logf: logf}, nil
}

// ProcessCapture returns the frame unmodified. Voice activity is detected
// via RMS energy thresholding as a fallback for the real APM VAD.
func (p *Processor) ProcessCapture(frame []float32) ([]float32, bool, error) {
	if len(frame) == 0 {
		return frame, false, nil
	}
	var sumSq float64
	for _, s := range frame {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(frame)))
	return frame, rms > energyThreshold, nil
}

// ProcessRender is a no-op in the stub build.
func (p *Processor) ProcessRender(frame []float32) error {
	return nil
}

// Close is a no-op in the stub build.
func (p *Processor) Close() {
	p.logf("apm: no-op processor closed")
}
