package audio

const (
	// SampleRate is the native sample rate for portaudio and APM processing.
	SampleRate = 48000

	// FrameSize is 10ms at 48kHz = 480 samples.
	FrameSize = 480

	// STTSampleRate is the sample rate expected by Whisper-based STT.
	STTSampleRate = 16000

	// TTSKokoroRate is the output sample rate for Kokoro TTS models.
	TTSKokoroRate = 24000

	// TTSPiperRate is the output sample rate for Piper/GLaDOS TTS models.
	TTSPiperRate = 22050

	// TTSPocketRate is the output sample rate for PocketTTS.
	TTSPocketRate = 24000
)
