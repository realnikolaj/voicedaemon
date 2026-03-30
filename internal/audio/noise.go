package audio

import (
	"encoding/json"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	noiseFFTSize    = 512 // FFT window size (matches portaudio frame + some overlap)
	noiseProfileDir = ".voicedaemon/profiles"
)

// NoiseProfile holds a calibrated noise floor spectrum for spectral subtraction.
type NoiseProfile struct {
	Name       string    `json:"name"`
	Created    time.Time `json:"created"`
	SampleRate int       `json:"sample_rate"`
	FFTSize    int       `json:"fft_size"`
	// MagnitudeSpectrum holds the average magnitude of each FFT bin during silence.
	MagnitudeSpectrum []float64 `json:"magnitude_spectrum"`
}

// CalibrateNoiseProfile computes an average noise spectrum from silence frames.
// Each frame should be a portaudio capture frame (e.g. 480 samples at 48kHz).
func CalibrateNoiseProfile(name string, frames [][]float32, sampleRate int) (*NoiseProfile, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("noise: no frames to calibrate")
	}

	fft := fourier.NewFFT(noiseFFTSize)
	numBins := noiseFFTSize/2 + 1
	avgMag := make([]float64, numBins)
	count := 0

	buf := make([]float64, noiseFFTSize)

	for _, frame := range frames {
		// Process in FFT-sized windows.
		for offset := 0; offset+noiseFFTSize <= len(frame); offset += noiseFFTSize {
			for i := 0; i < noiseFFTSize; i++ {
				buf[i] = float64(frame[offset+i])
			}

			coeffs := fft.Coefficients(nil, buf)

			for i := 0; i < numBins; i++ {
				avgMag[i] += cmplx.Abs(coeffs[i])
			}
			count++
		}
	}

	if count == 0 {
		return nil, fmt.Errorf("noise: frames too short for FFT size %d", noiseFFTSize)
	}

	for i := range avgMag {
		avgMag[i] /= float64(count)
	}

	return &NoiseProfile{
		Name:              name,
		Created:           time.Now(),
		SampleRate:        sampleRate,
		FFTSize:           noiseFFTSize,
		MagnitudeSpectrum: avgMag,
	}, nil
}

// Save writes the profile to ~/.voicedaemon/profiles/<name>.json.
func (p *NoiseProfile) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, noiseProfileDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, p.Name+".json")
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadNoiseProfile reads a named profile from disk.
func LoadNoiseProfile(name string) (*NoiseProfile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, noiseProfileDir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("noise: profile %q not found: %w", name, err)
	}
	var p NoiseProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("noise: parse profile %q: %w", name, err)
	}
	return &p, nil
}

// ListNoiseProfiles returns the names of all saved profiles.
func ListNoiseProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, noiseProfileDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name()[:len(e.Name())-5])
		}
	}
	return names, nil
}

// NoiseReducer applies real-time spectral subtraction using a calibrated profile.
type NoiseReducer struct {
	profile *NoiseProfile
	fft     *fourier.FFT
	fftSize int
	// Oversubtraction factor: higher = more aggressive noise removal.
	// 1.0 = exact subtraction, 2.0 = double. Default 1.5.
	Alpha float64
	// Spectral floor: minimum magnitude to prevent musical noise.
	// 0.01 is a good default.
	Beta float64
}

// NewNoiseReducer creates a reducer from a calibrated profile.
func NewNoiseReducer(profile *NoiseProfile) *NoiseReducer {
	return &NoiseReducer{
		profile: profile,
		fft:     fourier.NewFFT(profile.FFTSize),
		fftSize: profile.FFTSize,
		Alpha:   1.5,
		Beta:    0.01,
	}
}

// Process applies spectral subtraction to a frame of audio.
// Returns the cleaned frame (same length as input).
func (r *NoiseReducer) Process(frame []float32) []float32 {
	out := make([]float32, len(frame))

	buf := make([]float64, r.fftSize)
	outBuf := make([]float64, r.fftSize)

	for offset := 0; offset+r.fftSize <= len(frame); offset += r.fftSize {
		for i := 0; i < r.fftSize; i++ {
			buf[i] = float64(frame[offset+i])
		}

		coeffs := r.fft.Coefficients(nil, buf)

		numBins := r.fftSize/2 + 1
		for i := 0; i < numBins && i < len(r.profile.MagnitudeSpectrum); i++ {
			mag := cmplx.Abs(coeffs[i])
			phase := cmplx.Phase(coeffs[i])

			// Spectral subtraction with floor.
			cleanMag := mag - r.Alpha*r.profile.MagnitudeSpectrum[i]
			if cleanMag < r.Beta*mag {
				cleanMag = r.Beta * mag
			}

			coeffs[i] = cmplx.Rect(cleanMag, phase)
		}

		r.fft.Sequence(outBuf, coeffs)

		for i := 0; i < r.fftSize; i++ {
			out[offset+i] = float32(outBuf[i]) / float32(r.fftSize)
		}
	}

	// Copy any remainder that didn't fill a full FFT window.
	remainder := len(frame) % r.fftSize
	if remainder > 0 {
		copy(out[len(frame)-remainder:], frame[len(frame)-remainder:])
	}

	return out
}

// EQ applies a simple high-pass filter at the given cutoff frequency
// to remove low-frequency rumble (AC hum, handling noise).
// Uses a single-pole IIR filter for minimal latency.
type HighPassFilter struct {
	cutoff float64
	rate   int
	alpha  float64
	prev   float32
	prevIn float32
}

// NewHighPassFilter creates a high-pass filter at the given cutoff (Hz).
func NewHighPassFilter(cutoffHz float64, sampleRate int) *HighPassFilter {
	rc := 1.0 / (2.0 * math.Pi * cutoffHz)
	dt := 1.0 / float64(sampleRate)
	alpha := rc / (rc + dt)
	return &HighPassFilter{
		cutoff: cutoffHz,
		rate:   sampleRate,
		alpha:  alpha,
	}
}

// Process applies the high-pass filter to a frame in-place.
func (f *HighPassFilter) Process(frame []float32) {
	a := float32(f.alpha)
	for i, s := range frame {
		frame[i] = a * (f.prev + s - f.prevIn)
		f.prevIn = s
		f.prev = frame[i]
	}
}
