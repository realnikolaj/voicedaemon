package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestResample(t *testing.T) {
	tests := []struct {
		name    string
		srcRate int
		dstRate int
		inLen   int
		wantLen int
	}{
		{
			name:    "48kHz to 16kHz (3:1 downsample)",
			srcRate: 48000,
			dstRate: 16000,
			inLen:   4800,
			wantLen: 1600,
		},
		{
			name:    "24kHz to 48kHz (1:2 upsample)",
			srcRate: 24000,
			dstRate: 48000,
			inLen:   2400,
			wantLen: 4800,
		},
		{
			name:    "22050Hz to 48kHz upsample",
			srcRate: 22050,
			dstRate: 48000,
			inLen:   2205,
			wantLen: 4800,
		},
		{
			name:    "same rate passthrough",
			srcRate: 48000,
			dstRate: 48000,
			inLen:   480,
			wantLen: 480,
		},
		{
			name:    "empty input",
			srcRate: 48000,
			dstRate: 16000,
			inLen:   0,
			wantLen: 0,
		},
		{
			name:    "16kHz to 48kHz (1:3 upsample)",
			srcRate: 16000,
			dstRate: 48000,
			inLen:   1600,
			wantLen: 4800,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make([]float32, tt.inLen)
			for i := range in {
				in[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(tt.srcRate)))
			}

			out := Resample(in, tt.srcRate, tt.dstRate)

			if len(out) != tt.wantLen {
				t.Errorf("Resample(%d→%d): got %d samples, want %d", tt.srcRate, tt.dstRate, len(out), tt.wantLen)
			}
		})
	}
}

func TestResamplePreservesTone(t *testing.T) {
	srcRate := 48000
	dstRate := 16000
	freq := 440.0
	durationSamples := srcRate // 1 second

	in := make([]float32, durationSamples)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / float64(srcRate)))
	}

	out := Resample(in, srcRate, dstRate)

	// Verify output is bounded [-1, 1]
	for i, s := range out {
		if s < -1.01 || s > 1.01 {
			t.Errorf("sample %d out of range: %f", i, s)
			break
		}
	}
}

func TestResampleS16LE(t *testing.T) {
	tests := []struct {
		name    string
		srcRate int
		dstRate int
		inLen   int
		wantLen int
	}{
		{
			name:    "48kHz to 16kHz bytes",
			srcRate: 48000,
			dstRate: 16000,
			inLen:   960,
			wantLen: 320,
		},
		{
			name:    "24kHz to 48kHz bytes",
			srcRate: 24000,
			dstRate: 48000,
			inLen:   480,
			wantLen: 960,
		},
		{
			name:    "same rate passthrough",
			srcRate: 48000,
			dstRate: 48000,
			inLen:   960,
			wantLen: 960,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			numSamples := tt.inLen / 2
			data := make([]byte, tt.inLen)
			for i := range numSamples {
				v := int16(float64(math.MaxInt16) * math.Sin(2*math.Pi*440.0*float64(i)/float64(tt.srcRate)))
				binary.LittleEndian.PutUint16(data[i*2:i*2+2], uint16(v))
			}

			out := ResampleS16LE(data, tt.srcRate, tt.dstRate)

			wantBytes := tt.wantLen
			if len(out) != wantBytes {
				t.Errorf("ResampleS16LE(%d→%d): got %d bytes, want %d", tt.srcRate, tt.dstRate, len(out), wantBytes)
			}
		})
	}
}
