package audio

import (
	"encoding/binary"
	"math"
)

// Resample performs linear interpolation resampling of float32 audio samples.
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
