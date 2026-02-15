// Package capm provides Go bindings to the WebRTC AudioProcessing C++ wrapper.
// This package is only imported when building without the noapm tag.
// Isolating cgo and C++ source here prevents the Go tool from rejecting
// C++ files in packages that conditionally disable cgo.
package capm

/*
#cgo pkg-config: webrtc-audio-processing-2
#cgo CXXFLAGS: -std=c++17 -DWEBRTC_POSIX -Wno-nullability-completeness
#include "apm_wrapper.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Handle is an opaque APM processor handle.
type Handle struct {
	ptr unsafe.Pointer
}

// CreateWithAEC creates an APM instance with echo cancellation (AEC3).
// NS: high, AGC2: adaptive digital, high-pass: on, AEC3: on.
func CreateWithAEC() (*Handle, error) {
	p := C.apm_create_with_aec()
	if p == nil {
		return nil, errors.New("apm_create_with_aec failed")
	}
	return &Handle{ptr: p}, nil
}

// ProcessCapture processes a 10ms capture frame (480 float32 samples at 48kHz mono).
// Samples are modified in-place. Returns 1 if voice detected, 0 if not, -1 on error.
func (h *Handle) ProcessCapture(samples []float32, numSamples int) (int, error) {
	if h.ptr == nil {
		return -1, errors.New("handle closed")
	}
	ret := C.apm_process_capture(h.ptr, (*C.float)(unsafe.Pointer(&samples[0])), C.int(numSamples))
	return int(ret), nil
}

// ProcessRender feeds a 10ms render (speaker) frame as AEC reference signal.
// Must be called BEFORE the corresponding ProcessCapture.
func (h *Handle) ProcessRender(samples []float32, numSamples int) error {
	if h.ptr == nil {
		return errors.New("handle closed")
	}
	ret := C.apm_process_render(h.ptr, (*C.float)(unsafe.Pointer(&samples[0])), C.int(numSamples))
	if ret < 0 {
		return errors.New("apm_process_render failed")
	}
	return nil
}

// Destroy frees the APM instance. Safe to call with nil handle.
func (h *Handle) Destroy() {
	if h.ptr != nil {
		C.apm_destroy(h.ptr)
		h.ptr = nil
	}
}
