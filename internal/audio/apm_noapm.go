//go:build noapm

package audio

// Processor is a no-op stub when built without libwebrtc-audio-processing.
// Audio passes through unmodified and hasVoice is always false.
type Processor struct {
	logf func(string, ...any)
}

// NewProcessor creates a no-op processor stub.
func NewProcessor(logf func(string, ...any)) (*Processor, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf("apm: using no-op stub (built with -tags noapm)")
	return &Processor{logf: logf}, nil
}

// ProcessCapture returns the frame unmodified with hasVoice=false.
func (p *Processor) ProcessCapture(frame []float32) ([]float32, bool, error) {
	return frame, false, nil
}

// ProcessRender is a no-op in the stub build.
func (p *Processor) ProcessRender(frame []float32) error {
	return nil
}

// Close is a no-op in the stub build.
func (p *Processor) Close() {
	p.logf("apm: no-op processor closed")
}
