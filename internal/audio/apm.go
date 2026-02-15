//go:build !noapm

package audio

import (
	"errors"
	"fmt"

	"github.com/realnikolaj/voicedaemon/internal/capm"
)

// Processor wraps WebRTC AudioProcessing via cgo for NS, AGC, VAD, and AEC.
type Processor struct {
	handle *capm.Handle
	logf   func(string, ...any)
}

// NewProcessor creates an APM processor with AEC3 enabled.
// NS: high, AGC2: adaptive digital, high-pass: on, AEC3: on.
func NewProcessor(logf func(string, ...any)) (*Processor, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	h, err := capm.CreateWithAEC()
	if err != nil {
		return nil, fmt.Errorf("apm: %w", err)
	}
	logf("apm: processor created (NS=High, AGC2=AdaptiveDigital, HPF=on, AEC3=on)")
	return &Processor{handle: h, logf: logf}, nil
}

// ProcessCapture runs a 10ms capture frame (480 float32 samples at 48kHz mono)
// through the APM pipeline. Returns a new slice with processed audio and whether
// voice activity was detected.
func (p *Processor) ProcessCapture(frame []float32) ([]float32, bool, error) {
	if p.handle == nil {
		return nil, false, errors.New("apm: processor closed")
	}
	if len(frame) != FrameSize {
		return nil, false, fmt.Errorf("apm: expected %d samples, got %d", FrameSize, len(frame))
	}

	// Copy frame before passing to C — the C call modifies in-place.
	clean := make([]float32, FrameSize)
	copy(clean, frame)

	ret, err := p.handle.ProcessCapture(clean, FrameSize)
	if err != nil {
		return nil, false, fmt.Errorf("apm: process capture: %w", err)
	}
	if ret < 0 {
		return nil, false, errors.New("apm: process capture failed")
	}
	return clean, ret == 1, nil
}

// ProcessRender feeds a 10ms speaker (render) frame to the AEC as reference signal.
// Must be called BEFORE the corresponding ProcessCapture for echo cancellation.
func (p *Processor) ProcessRender(frame []float32) error {
	if p.handle == nil {
		return errors.New("apm: processor closed")
	}
	if len(frame) != FrameSize {
		return fmt.Errorf("apm: expected %d samples, got %d", FrameSize, len(frame))
	}

	if err := p.handle.ProcessRender(frame, FrameSize); err != nil {
		return fmt.Errorf("apm: process render: %w", err)
	}
	return nil
}

// Close destroys the APM instance. Safe to call multiple times.
func (p *Processor) Close() {
	if p.handle != nil {
		p.handle.Destroy()
		p.handle = nil
		p.logf("apm: processor closed")
	}
}
