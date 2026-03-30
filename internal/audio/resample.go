package audio

import (
	"encoding/binary"
	"math"
)

// Resample performs linear interpolation resampling of float32 audio samples.
// For higher quality decimation, use FIRDecimator instead.
func Resample(samples []float32, srcRate, dstRate int) []float32 {
	if srcRate == dstRate || len(samples) == 0 {
		return samples
	}

	ratio := float64(srcRate) / float64(dstRate)
	outLen := int(math.Ceil(float64(len(samples)) / ratio))
	out := make([]float32, outLen)

	for i := range out {
		srcIdx := float64(i) * ratio
		idx := int(srcIdx)
		frac := float32(srcIdx - float64(idx))

		if idx+1 < len(samples) {
			out[i] = samples[idx]*(1-frac) + samples[idx+1]*frac
		} else if idx < len(samples) {
			out[i] = samples[idx]
		}
	}

	return out
}

// FIRDecimator performs anti-aliased downsampling using a windowed-sinc FIR
// low-pass filter followed by decimation. Maintains filter state across
// calls for seamless real-time frame processing.
type FIRDecimator struct {
	factor int       // decimation factor (e.g. 3 for 48k→16k)
	taps   []float64 // FIR filter coefficients (symmetric)
	delay  []float32 // overlap buffer for filter state between frames
}

// NewFIRDecimator creates a decimator that downsamples by the given factor.
// numTaps should be odd for a symmetric filter; 33 is a good default.
// cutoff is the normalised cutoff frequency (0-1, where 1 = Nyquist).
// For factor=3 (48k→16k), cutoff ≈ 0.333 keeps everything below 8kHz.
func NewFIRDecimator(factor, numTaps int, cutoff float64) *FIRDecimator {
	if numTaps%2 == 0 {
		numTaps++
	}
	taps := designLowPass(numTaps, cutoff)
	return &FIRDecimator{
		factor: factor,
		taps:   taps,
		delay:  make([]float32, numTaps-1),
	}
}

// Process decimates the input frame and returns the downsampled output.
// Input length must be divisible by the decimation factor.
func (d *FIRDecimator) Process(in []float32) []float32 {
	numTaps := len(d.taps)
	halfTaps := numTaps / 2

	// Prepend delay line to input for continuous filtering across frames.
	extended := make([]float32, len(d.delay)+len(in))
	copy(extended, d.delay)
	copy(extended[len(d.delay):], in)

	outLen := len(in) / d.factor
	out := make([]float32, outLen)

	for i := 0; i < outLen; i++ {
		centre := len(d.delay) + i*d.factor
		var sum float64
		for t := 0; t < numTaps; t++ {
			idx := centre - halfTaps + t
			if idx >= 0 && idx < len(extended) {
				sum += float64(extended[idx]) * d.taps[t]
			}
		}
		out[i] = float32(sum)
	}

	// Save the tail of input as delay for the next frame.
	if len(in) >= numTaps-1 {
		copy(d.delay, in[len(in)-(numTaps-1):])
	} else {
		shift := numTaps - 1 - len(in)
		copy(d.delay, d.delay[len(d.delay)-shift:])
		copy(d.delay[shift:], in)
	}

	return out
}

// designLowPass creates a windowed-sinc FIR low-pass filter with Hamming window.
func designLowPass(numTaps int, cutoff float64) []float64 {
	taps := make([]float64, numTaps)
	mid := numTaps / 2

	for i := 0; i < numTaps; i++ {
		n := float64(i - mid)
		if n == 0 {
			taps[i] = cutoff
		} else {
			taps[i] = math.Sin(math.Pi*cutoff*n) / (math.Pi * n)
		}
		// Hamming window
		taps[i] *= 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(numTaps-1))
	}

	// Normalise for unity DC gain.
	var sum float64
	for _, t := range taps {
		sum += t
	}
	for i := range taps {
		taps[i] /= sum
	}
	return taps
}

// ResampleS16LE resamples raw PCM s16le byte data between sample rates.
func ResampleS16LE(data []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate || len(data) < 2 {
		return data
	}

	numSamples := len(data) / 2
	samples := make([]float32, numSamples)
	for i := range numSamples {
		s := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		samples[i] = float32(s) / 32768.0
	}

	resampled := Resample(samples, srcRate, dstRate)

	out := make([]byte, len(resampled)*2)
	for i, s := range resampled {
		v := int16(s * 32767.0)
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], uint16(v))
	}

	return out
}
