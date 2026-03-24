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

	// SileroSampleRate is the sample rate expected by Silero VAD (16kHz mono).
	SileroSampleRate = 16000

	// SileroFrameSize is the window size for Silero VAD v5 at 16kHz (512 samples = 32ms).
	SileroFrameSize = 512

	// DefaultSpeechThreshold is the default Silero speech probability threshold.
	DefaultSpeechThreshold = 0.35

	// DefaultSileroModelPath is the default filesystem path for the Silero ONNX model.
	DefaultSileroModelPath = "~/.voicedaemon/silero_vad.onnx"

	// SileroModelURL is the download URL for the Silero VAD v5 ONNX model.
	SileroModelURL = "https://github.com/snakers4/silero-vad/raw/master/src/silero_vad/data/silero_vad.onnx"
)
