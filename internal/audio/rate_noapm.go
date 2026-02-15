//go:build noapm

package audio

const (
	// OutputSampleRate is the portaudio output stream sample rate.
	// Without APM, output at 24kHz to match Kokoro TTS native rate
	// and avoid resampling artifacts.
	OutputSampleRate = 24000

	// OutputFrameSize is 10ms at the output sample rate.
	OutputFrameSize = 240
)
