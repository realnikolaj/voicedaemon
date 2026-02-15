//go:build !noapm

package audio

/*
#cgo pkg-config: webrtc-audio-processing-2
#cgo CXXFLAGS: -std=c++17 -DWEBRTC_POSIX -Wno-nullability-completeness
#include "apm_wrapper.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// APMProcessor wraps the WebRTC AudioProcessing module via cgo.
type APMProcessor struct {
	handle unsafe.Pointer
}

// NewAPMProcessor creates a new APM instance configured for capture processing.
// NS: high, AGC2: adaptive digital, high-pass: on, AEC: off.
func NewAPMProcessor() (*APMProcessor, error) {
	h := C.apm_create()
	if h == nil {
		return nil, errors.New("apm_create failed")
	}
	return &APMProcessor{handle: h}, nil
}

// NewAPMProcessorWithAEC creates a new APM instance with echo cancellation (AEC3).
// NS: high, AGC2: adaptive digital, high-pass: on, AEC3: on.
// Use ProcessRender() to feed speaker reference frames before ProcessFrame().
func NewAPMProcessorWithAEC() (*APMProcessor, error) {
	h := C.apm_create_with_aec()
	if h == nil {
		return nil, errors.New("apm_create_with_aec failed")
	}
	return &APMProcessor{handle: h}, nil
}

// ProcessFrame runs a 10ms capture frame (480 float32 samples at 48kHz mono)
// through the APM pipeline. Samples are modified in-place.
// Returns whether voice was detected in this frame.
func (p *APMProcessor) ProcessFrame(samples []float32) (bool, error) {
	if p.handle == nil {
		return false, errors.New("APM processor closed")
	}
	if len(samples) != FramesPerBuffer {
		return false, fmt.Errorf("expected %d samples, got %d", FramesPerBuffer, len(samples))
	}

	ret := C.apm_process_capture(p.handle, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)))
	if ret < 0 {
		return false, errors.New("apm_process_capture failed")
	}
	return ret == 1, nil
}

// ProcessRender feeds a 10ms speaker (render) frame to the AEC as reference signal.
// Must be called BEFORE the corresponding ProcessFrame() for echo cancellation.
// Samples are 480 float32 at 48kHz mono. The input is not modified.
func (p *APMProcessor) ProcessRender(samples []float32) error {
	if p.handle == nil {
		return errors.New("APM processor closed")
	}
	if len(samples) != FramesPerBuffer {
		return fmt.Errorf("expected %d samples, got %d", FramesPerBuffer, len(samples))
	}

	ret := C.apm_process_render(p.handle, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)))
	if ret < 0 {
		return errors.New("apm_process_render failed")
	}
	return nil
}

// Close destroys the APM instance. Safe to call multiple times.
func (p *APMProcessor) Close() {
	if p.handle != nil {
		C.apm_destroy(p.handle)
		p.handle = nil
	}
}

// SetStreamDelay sets the stream delay in milliseconds for AEC alignment.
func (p *APMProcessor) SetStreamDelay(delayMs int) error {
	if p.handle == nil {
		return errors.New("APM processor closed")
	}
	C.apm_set_stream_delay(p.handle, C.int(delayMs))
	return nil
}

// GetSpeechProbability returns voice detection status:
// 1.0 if voice detected, 0.0 if not, -1.0 if not available (v2 deprecated).
func (p *APMProcessor) GetSpeechProbability() float64 {
	if p.handle == nil {
		return -1.0
	}
	return float64(C.apm_get_speech_probability(p.handle))
}
