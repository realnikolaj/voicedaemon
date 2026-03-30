package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/gordonklaus/portaudio"
	"github.com/realnikolaj/voicedaemon/internal/audio"
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
	VADSilenceMs   int     `name:"vad-silence" help:"Server-side silence duration before utterance cut (ms)." default:"550" env:"VOICEDAEMON_VAD_SILENCE"`
	NoiseProfile   string  `name:"noise-profile" help:"Load a calibrated noise profile for noise reduction." default:"" env:"VOICEDAEMON_NOISE_PROFILE"`
	Calibrate      string  `name:"calibrate" help:"Record silence and save a noise profile with this name, then exit." default:""`
	CalibrateDur   int     `name:"calibrate-duration" help:"Calibration recording duration in seconds." default:"3" hidden:""`
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

	// Handle --calibrate: record silence, save profile, exit.
	if cli.Calibrate != "" {
		if err := runCalibration(cli.Calibrate, cli.CalibrateDur, logf); err != nil {
			logf("fatal: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle --noise-profile list
	if cli.NoiseProfile == "list" {
		profiles, err := audio.ListNoiseProfiles()
		if err != nil {
			logf("fatal: %v", err)
			os.Exit(1)
		}
		if len(profiles) == 0 {
			fmt.Println("no noise profiles found — use --calibrate <name> to create one")
		} else {
			for _, p := range profiles {
				fmt.Println(p)
			}
		}
		os.Exit(0)
	}

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
		NoiseProfile:  cli.NoiseProfile,
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

func runCalibration(name string, durationSec int, logf func(string, ...any)) error {
	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("portaudio init: %w", err)
	}
	defer portaudio.Terminate()

	frameSize := audio.FrameSize // 480 samples at 48kHz
	buf := make([]float32, frameSize)
	stream, err := portaudio.OpenDefaultStream(1, 0, float64(audio.SampleRate), frameSize, buf)
	if err != nil {
		return fmt.Errorf("open mic: %w", err)
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		return fmt.Errorf("start mic: %w", err)
	}

	fmt.Printf("Calibrating noise profile %q — stay silent for %d seconds...\n", name, durationSec)

	dur := time.Duration(durationSec) * time.Second
	deadline := time.Now().Add(dur)
	var frames [][]float32

	for time.Now().Before(deadline) {
		if err := stream.Read(); err != nil {
			return fmt.Errorf("read mic: %w", err)
		}
		frame := make([]float32, len(buf))
		copy(frame, buf)
		frames = append(frames, frame)
	}

	stream.Stop()

	profile, err := audio.CalibrateNoiseProfile(name, frames, audio.SampleRate)
	if err != nil {
		return fmt.Errorf("calibrate: %w", err)
	}

	if err := profile.Save(); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}

	fmt.Printf("Saved noise profile %q (%d frames, %ds)\n", name, len(frames), durationSec)
	return nil
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
