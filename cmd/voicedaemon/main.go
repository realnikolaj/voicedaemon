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
	Version      kong.VersionFlag `name:"version" help:"Print version."`
	Port         int              `help:"HTTP server port." default:"5111" env:"DAEMON_PORT" hidden:""`
	SocketPath   string           `help:"Unix socket path." default:"/tmp/voice-daemon.sock" env:"STT_SOCKET_PATH" hidden:""`
	SpeachesURL  string           `help:"Speaches server URL (STT + TTS)." default:"http://localhost:34331" env:"SPEACHES_URL"`
	PocketTTSURL string           `name:"pocket-tts-url" help:"PocketTTS server URL." default:"http://localhost:49112" env:"POCKET_TTS_URL"`
	STTModel       string  `help:"Whisper model for transcription." default:"deepdml/faster-whisper-large-v3-turbo-ct2" env:"STT_MODEL"`
	STTLanguage    string  `help:"STT language code." default:"en" env:"STT_LANGUAGE" hidden:""`
	VADThreshold   float64 `name:"vad-threshold" help:"Server-side Silero VAD speech probability (0-1)." default:"0.9" env:"VOICEDAEMON_VAD_THRESHOLD"`
	VADSilenceMs   int     `name:"vad-silence" help:"Server-side silence duration before utterance cut (ms)." default:"1500" env:"VOICEDAEMON_VAD_SILENCE"`
	SilenceGapMS   int     `name:"silence-gap" help:"Local VAD silence gap in ms (batch fallback only)." default:"1100" env:"VOICEDAEMON_SILENCE_GAP" hidden:""`
	TTSLog         string  `name:"tts-log" help:"TTS JSONL log path." default:"" env:"VOICEDAEMON_TTS_LOG"`
	Debug          bool    `help:"Enable debug logging." default:"false" env:"VOICEDAEMON_DEBUG"`

	// TTS configuration — used by the /speak HTTP endpoint and MCP voice tools.
	SpeachesModel string `help:"Speaches TTS model." default:"speaches-ai/Kokoro-82M-v1.0-ONNX" env:"SPEACHES_MODEL" hidden:""`
	SpeachesVoice string `help:"Speaches TTS voice." default:"af_heart" env:"SPEACHES_VOICE" hidden:""`
	PocketVoice   string `help:"PocketTTS voice." default:"alba" env:"POCKET_TTS_VOICE" hidden:""`
}

func main() {
	var cli CLI
	kong.Parse(&cli,
		kong.Name("voicedaemon"),
		kong.Description("Standalone STT+TTS daemon with portaudio capture, VAD, and echo cancellation."),
		kong.Vars{"version": version},
	)

	logf := makeLogf(cli.Debug)

	cfg := daemon.Config{
		Port:          cli.Port,
		SocketPath:    cli.SocketPath,
		SpeachesURL:   cli.SpeachesURL,
		PocketTTSURL:  cli.PocketTTSURL,
		STTModel:      cli.STTModel,
		STTLanguage:   cli.STTLanguage,
		SpeachesModel: cli.SpeachesModel,
		SpeachesVoice: cli.SpeachesVoice,
		PocketVoice:   cli.PocketVoice,
		SilenceGapMS:  cli.SilenceGapMS,
		VADThreshold:  cli.VADThreshold,
		VADSilenceMs:  cli.VADSilenceMs,
		TTSLogPath:    cli.TTSLog,
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
