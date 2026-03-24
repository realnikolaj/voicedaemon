//go:build !noapm && !silero

package audio

const (
	// OutputSampleRate is the portaudio output stream sample rate.
	// With real APM, output must match capture at 48kHz for AEC symmetry.
	OutputSampleRate = 48000

	// OutputFrameSize is 10ms at the output sample rate.
	OutputFrameSize = 480
)
