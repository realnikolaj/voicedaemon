package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gordonklaus/portaudio"
	"github.com/realnikolaj/voicedaemon/internal/audio"
	"github.com/realnikolaj/voicedaemon/internal/stt"
	"github.com/realnikolaj/voicedaemon/internal/tts"
)

// Config holds all configuration for the daemon.
type Config struct {
	Port          int
	SocketPath    string
	SpeachesURL   string
	PocketTTSURL  string
	STTURL        string
	STTModel      string
	STTLanguage   string
	SpeachesModel string
	SpeachesVoice string
	PocketVoice   string
	TTSLogPath    string
	Debug         bool
	Logf          func(string, ...any)
}

// DefaultConfig returns daemon config with standard defaults.
func DefaultConfig() Config {
	return Config{
		Port:          5111,
		SocketPath:    "/tmp/voice-daemon.sock",
		SpeachesURL:   "http://localhost:34331",
		PocketTTSURL:  "http://localhost:49112",
		STTURL:        "http://localhost:34331",
		STTModel:      "deepdml/faster-whisper-large-v3-turbo-ct2",
		STTLanguage:   "en",
		SpeachesModel: "speaches-ai/Kokoro-82M-v1.0-ONNX",
		SpeachesVoice: "af_heart",
		PocketVoice:   "alba",
	}
}

// Daemon owns all subsystems and orchestrates the voice daemon lifecycle.
type Daemon struct {
	cfg          Config
	logf         func(string, ...any)
	speaker      *audio.Speaker
	pipeline     *audio.Pipeline
	sttClient    *stt.Client
	ttsClient    *tts.Client
	ttsQueue     *tts.Queue
	ttsLogWriter *tts.TTSLogWriter
	socketSrv    *SocketServer
	httpSrv      *HTTPServer

	mu          sync.Mutex
	transcripts []string
}

// New creates a new daemon with all subsystems wired together.
func New(cfg Config) (*Daemon, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// Speaker
	spkCfg := audio.DefaultSpeakerConfig()
	spkCfg.Logf = logf
	speaker := audio.NewSpeaker(spkCfg)

	// Audio pipeline
	pipeCfg := audio.DefaultPipelineConfig()
	pipeCfg.Logf = logf
	pipeline, err := audio.NewPipeline(pipeCfg, speaker)
	if err != nil {
		return nil, fmt.Errorf("daemon: create pipeline: %w", err)
	}

	// STT client
	sttCfg := stt.ClientConfig{
		URL:      cfg.STTURL,
		Model:    cfg.STTModel,
		Language: cfg.STTLanguage,
		Logf:     logf,
	}
	sttClient := stt.NewClient(sttCfg)

	// TTS client
	ttsCfg := tts.ClientConfig{
		SpeachesURL:   cfg.SpeachesURL,
		PocketTTSURL:  cfg.PocketTTSURL,
		SpeachesModel: cfg.SpeachesModel,
		SpeachesVoice: cfg.SpeachesVoice,
		PocketVoice:   cfg.PocketVoice,
		Logf:          logf,
	}
	ttsClient := tts.NewClient(ttsCfg)

	// TTS log writer (optional)
	var ttsLogWriter *tts.TTSLogWriter
	if cfg.TTSLogPath != "" {
		ttsLogWriter, err = tts.NewTTSLogWriter(cfg.TTSLogPath)
		if err != nil {
			return nil, fmt.Errorf("daemon: create tts log writer: %w", err)
		}
		logf("daemon: tts log enabled: %s", cfg.TTSLogPath)
	}

	// TTS queue
	queueCfg := tts.QueueConfig{
		Client:       ttsClient,
		Speaker:      speaker,
		RenderFeeder: pipeline,
		LogWriter:    ttsLogWriter,
		Logf:         logf,
	}
	ttsQueue := tts.NewQueue(queueCfg)

	// Socket server
	sockCfg := SocketConfig{
		Path: cfg.SocketPath,
		Logf: logf,
	}
	socketSrv := NewSocketServer(sockCfg)

	// HTTP server
	httpCfg := HTTPConfig{
		Port:         cfg.Port,
		SpeachesURL:  cfg.SpeachesURL,
		PocketTTSURL: cfg.PocketTTSURL,
		STTURL:       cfg.STTURL,
		SocketPath:   cfg.SocketPath,
		Logf:         logf,
	}
	httpSrv := NewHTTPServer(httpCfg, ttsQueue)

	d := &Daemon{
		cfg:          cfg,
		logf:         logf,
		speaker:      speaker,
		pipeline:     pipeline,
		sttClient:    sttClient,
		ttsClient:    ttsClient,
		ttsQueue:     ttsQueue,
		ttsLogWriter: ttsLogWriter,
		socketSrv:    socketSrv,
		httpSrv:      httpSrv,
	}

	// Wire socket callbacks
	socketSrv.SetCallbacks(d.onSocketStart, d.onSocketStop, d.onSocketCancel, d.onSocketStatus)

	return d, nil
}

// Run starts all subsystems and blocks until ctx is canceled.
func (d *Daemon) Run(ctx context.Context) error {
	d.logf("daemon: initializing portaudio")
	if err := portaudio.Initialize(); err != nil {
		return fmt.Errorf("daemon: portaudio init: %w", err)
	}
	defer func() {
		if err := portaudio.Terminate(); err != nil {
			d.logf("daemon: portaudio terminate error: %v", err)
		}
	}()

	// 1. Open speaker (persistent stream)
	d.logf("daemon: opening speaker")
	if err := d.speaker.Open(); err != nil {
		return fmt.Errorf("daemon: open speaker: %w", err)
	}

	// 2. Start audio pipeline with onUtterance callback
	d.logf("daemon: starting audio pipeline")
	if err := d.pipeline.Start(d.onUtterance); err != nil {
		return fmt.Errorf("daemon: start pipeline: %w", err)
	}

	// 3. Start TTS queue
	d.ttsQueue.Start()

	// 4. Start socket server
	d.logf("daemon: starting socket server on %s", d.cfg.SocketPath)
	if err := d.socketSrv.Start(ctx); err != nil {
		return fmt.Errorf("daemon: start socket: %w", err)
	}

	// 5. Start HTTP server
	d.logf("daemon: starting HTTP server on :%d", d.cfg.Port)
	if err := d.httpSrv.Start(ctx); err != nil {
		return fmt.Errorf("daemon: start http: %w", err)
	}

	d.logf("daemon: ready (http=:%d, socket=%s)", d.cfg.Port, d.cfg.SocketPath)

	// Block until context is canceled
	<-ctx.Done()

	d.logf("daemon: shutting down...")

	// Graceful shutdown in reverse order
	if err := d.httpSrv.Close(); err != nil {
		d.logf("daemon: http close error: %v", err)
	}

	if err := d.socketSrv.Close(); err != nil {
		d.logf("daemon: socket close error: %v", err)
	}

	d.ttsQueue.Stop()

	// Close TTS log writer after queue has drained
	if d.ttsLogWriter != nil {
		if err := d.ttsLogWriter.Close(); err != nil {
			d.logf("daemon: tts log close error: %v", err)
		}
	}

	if err := d.pipeline.Stop(); err != nil {
		d.logf("daemon: pipeline stop error: %v", err)
	}

	if err := d.speaker.Close(); err != nil {
		d.logf("daemon: speaker close error: %v", err)
	}

	d.logf("daemon: shutdown complete")
	return nil
}

// onUtterance is called by the audio pipeline when VAD detects a complete utterance.
func (d *Daemon) onUtterance(samples []float32) {
	text, err := d.sttClient.Transcribe(context.Background(), samples)
	if err != nil {
		d.logf("daemon: transcribe error: %v", err)
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	d.mu.Lock()
	d.transcripts = append(d.transcripts, text)
	d.mu.Unlock()

	d.socketSrv.PushTranscript(text)
}

// onSocketStart handles the "start" command from the socket.
func (d *Daemon) onSocketStart() {
	d.mu.Lock()
	d.transcripts = nil
	d.mu.Unlock()

	d.pipeline.StartListening()
	d.logf("daemon: recording started via socket")
}

// onSocketStop handles the "stop" command from the socket.
// Returns all accumulated transcripts joined with spaces.
func (d *Daemon) onSocketStop() string {
	d.pipeline.StopListening()

	d.mu.Lock()
	result := strings.Join(d.transcripts, " ")
	d.transcripts = nil
	d.mu.Unlock()

	d.logf("daemon: recording stopped via socket, transcript: %q", result)
	return result
}

// onSocketCancel handles the "cancel" command from the socket.
func (d *Daemon) onSocketCancel() {
	d.pipeline.StopListening()

	d.mu.Lock()
	d.transcripts = nil
	d.mu.Unlock()

	d.logf("daemon: recording cancelled via socket")
}

// onSocketStatus handles the "status" command from the socket.
func (d *Daemon) onSocketStatus() string {
	return d.pipeline.VADState().String()
}
