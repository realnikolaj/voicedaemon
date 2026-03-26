package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/realnikolaj/voicedaemon/internal/daemon"
)

var version = "0.2.8"

// CLI defines the Kong CLI flags for voicedaemon.
type CLI struct {
	Version       kong.VersionFlag `name:"version" help:"Print version."`
	Port          int              `help:"HTTP server port." default:"5111" env:"DAEMON_PORT"`
	SocketPath    string           `help:"Unix socket path for STT." default:"/tmp/voice-daemon.sock" env:"STT_SOCKET_PATH"`
	SpeachesURL   string           `help:"Speaches server URL (also used for STT unless --stt-url is set)." default:"http://localhost:34331" env:"SPEACHES_URL"`
	PocketTTSURL  string           `name:"pocket-tts-url" help:"PocketTTS server URL." default:"http://localhost:49112" env:"POCKET_TTS_URL"`
	STTURL        string           `name:"stt-url" help:"STT server URL (defaults to --speaches-url if not set)." default:"" env:"STT_URL"`
	STTModel      string           `help:"STT model name." default:"deepdml/faster-whisper-large-v3-turbo-ct2" env:"STT_MODEL"`
	STTLanguage   string           `help:"STT language code." default:"en" env:"STT_LANGUAGE"`
	SpeachesModel string           `help:"Speaches TTS model." default:"speaches-ai/Kokoro-82M-v1.0-ONNX" env:"SPEACHES_MODEL"`
	SpeachesVoice string           `help:"Speaches TTS voice." default:"af_heart" env:"SPEACHES_VOICE"`
	PocketVoice   string           `help:"PocketTTS voice." default:"alba" env:"POCKET_TTS_VOICE"`
	SilenceGapMS    int     `name:"silence-gap" help:"VAD silence gap in milliseconds before utterance ends." default:"1100" env:"VOICEDAEMON_SILENCE_GAP"`
	VADModel        string  `name:"vad-model" help:"Path to Silero VAD ONNX model (silero build only)." default:"~/.voicedaemon/silero_vad.onnx" env:"VOICEDAEMON_VAD_MODEL"`
	SpeechThreshold float64 `name:"speech-threshold" help:"Silero speech probability threshold (0.0-1.0)." default:"0.35" env:"VOICEDAEMON_SPEECH_THRESHOLD"`
	TTSLog          string  `name:"tts-log" help:"Path to TTS JSONL log file (empty=disabled)." default:"" env:"VOICEDAEMON_TTS_LOG"`
	Debug           bool    `help:"Enable debug logging." default:"false" env:"VOICEDAEMON_DEBUG"`
}

func main() {
	var cli CLI
	kong.Parse(&cli,
		kong.Name("voicedaemon"),
		kong.Description("Standalone STT+TTS daemon with portaudio capture, VAD, and echo cancellation."),
		kong.Vars{"version": version},
	)

	// Fall back to SpeachesURL when STT URL is not explicitly set.
	// One server, one URL. Override only when STT lives elsewhere.
	if cli.STTURL == "" {
		cli.STTURL = cli.SpeachesURL
	}

	logf := makeLogf(cli.Debug)

	cfg := daemon.Config{
		Port:          cli.Port,
		SocketPath:    cli.SocketPath,
		SpeachesURL:   cli.SpeachesURL,
		PocketTTSURL:  cli.PocketTTSURL,
		STTURL:        cli.STTURL,
		STTModel:      cli.STTModel,
		STTLanguage:   cli.STTLanguage,
		SpeachesModel: cli.SpeachesModel,
		SpeachesVoice: cli.SpeachesVoice,
		PocketVoice:   cli.PocketVoice,
		SilenceGapMS:    cli.SilenceGapMS,
		VADModelPath:    cli.VADModel,
		SpeechThreshold: cli.SpeechThreshold,
		TTSLogPath:      cli.TTSLog,
		Debug:         cli.Debug,
		Logf:          logf,
	}

	d, err := daemon.New(cfg)
	if err != nil {
		logf("fatal: %v", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logf("voicedaemon starting (port=%d, socket=%s)", cli.Port, cli.SocketPath)

	if err := d.Run(ctx); err != nil {
		logf("fatal: %v", err)
		os.Exit(1)
	}
}

func makeLogf(debug bool) func(string, ...any) {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	return func(format string, args ...any) {
		if !debug && len(format) > 0 {
			// In non-debug mode, only log lines that don't start with known debug prefixes
			for _, prefix := range []string{"apm:", "mic:", "speaker:", "vad:", "pipeline:", "stt:"} {
				if len(format) >= len(prefix) && format[:len(prefix)] == prefix {
					return
				}
			}
		}
		logger.Output(2, fmt.Sprintf(format, args...)) //nolint:errcheck // log output
	}
}
