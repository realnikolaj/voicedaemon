//go:build silero

package audio

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	onnx "github.com/yalue/onnxruntime_go"
)

const (
	sileroContextSize = 64  // context samples prepended to each window
	sileroStateSize   = 256 // 2 * 1 * 128 LSTM state
)

// Processor wraps the Silero VAD ONNX model for the silero build.
type Processor struct {
	logf      func(string, ...any)
	session   *onnx.AdvancedSession
	input     *onnx.Tensor[float32]
	state     *onnx.Tensor[float32]
	sr        *onnx.Tensor[int64]
	output    *onnx.Tensor[float32]
	newState  *onnx.Tensor[float32]

	context   []float32 // last 64 samples from previous window
	mu        sync.Mutex
	threshold float32
	buf       []float32
	lastVoice bool
	closed    bool
}

// NewProcessor creates a Silero VAD processor using ONNX Runtime directly.
func NewProcessor(cfg PipelineConfig) (*Processor, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	modelPath := cfg.VADModelPath
	if modelPath == "" {
		modelPath = DefaultSileroModelPath
	}
	if strings.HasPrefix(modelPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("apm: resolve home dir: %w", err)
		}
		modelPath = filepath.Join(home, modelPath[2:])
	}

	threshold := float32(cfg.SpeechThreshold)
	if threshold <= 0 {
		threshold = DefaultSpeechThreshold
	}

	// Download model if missing.
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		if err := downloadModel(modelPath, logf); err != nil {
			return nil, fmt.Errorf("apm: download silero model: %w", err)
		}
	}

	// Initialize ONNX Runtime.
	if !onnx.IsInitialized() {
		libPath := "/usr/local/lib/libonnxruntime.dylib"
		if _, err := os.Stat(libPath); os.IsNotExist(err) {
			libPath = "/usr/local/Cellar/onnxruntime/1.24.4_1/lib/libonnxruntime.dylib"
		}
		onnx.SetSharedLibraryPath(libPath)
		if err := onnx.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("apm: init onnx: %w", err)
		}
	}

	// Create session options.
	opts, err := onnx.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("apm: session options: %w", err)
	}
	defer opts.Destroy()
	opts.SetIntraOpNumThreads(1)
	opts.SetInterOpNumThreads(1)

	effectiveWindow := SileroFrameSize + sileroContextSize // 512 + 64 = 576

	// Input: [1, effectiveWindow]
	input, err := onnx.NewEmptyTensor[float32](onnx.NewShape(1, int64(effectiveWindow)))
	if err != nil {
		return nil, fmt.Errorf("apm: input tensor: %w", err)
	}

	// State: [2, 1, 128]
	state, err := onnx.NewEmptyTensor[float32](onnx.NewShape(2, 1, 128))
	if err != nil {
		input.Destroy()
		return nil, fmt.Errorf("apm: state tensor: %w", err)
	}

	// Sample rate: [1]
	sr, err := onnx.NewTensor[int64](onnx.NewShape(1), []int64{int64(SileroSampleRate)})
	if err != nil {
		input.Destroy()
		state.Destroy()
		return nil, fmt.Errorf("apm: sr tensor: %w", err)
	}

	// Output: [1, 1]
	output, err := onnx.NewEmptyTensor[float32](onnx.NewShape(1, 1))
	if err != nil {
		input.Destroy()
		state.Destroy()
		sr.Destroy()
		return nil, fmt.Errorf("apm: output tensor: %w", err)
	}

	// New state: [2, 1, 128]
	newState, err := onnx.NewEmptyTensor[float32](onnx.NewShape(2, 1, 128))
	if err != nil {
		input.Destroy()
		state.Destroy()
		sr.Destroy()
		output.Destroy()
		return nil, fmt.Errorf("apm: new state tensor: %w", err)
	}

	session, err := onnx.NewAdvancedSession(
		modelPath,
		[]string{"input", "state", "sr"},
		[]string{"output", "stateN"},
		[]onnx.Value{input, state, sr},
		[]onnx.Value{output, newState},
		opts,
	)
	if err != nil {
		input.Destroy()
		state.Destroy()
		sr.Destroy()
		output.Destroy()
		newState.Destroy()
		return nil, fmt.Errorf("apm: create session: %w", err)
	}

	logf("apm: using Silero VAD (model=%s, threshold=%.2f, built with -tags silero)", modelPath, threshold)

	return &Processor{
		logf:      logf,
		session:   session,
		input:     input,
		state:     state,
		sr:        sr,
		output:    output,
		newState:  newState,
		context:   make([]float32, sileroContextSize),
		threshold: threshold,
		buf:       make([]float32, 0, SileroFrameSize*4),
	}, nil
}

// predict runs one Silero inference on a 512-sample window.
func (p *Processor) predict(window []float32) (float32, error) {
	// Build input: context (64 samples) + window (512 samples)
	inputData := p.input.GetData()
	for i := range inputData {
		inputData[i] = 0
	}
	copy(inputData, p.context)
	copy(inputData[sileroContextSize:], window)

	if err := p.session.Run(); err != nil {
		return 0, fmt.Errorf("inference: %w", err)
	}

	prob := p.output.GetData()[0]

	// Update state: copy newState → state
	copy(p.state.GetData(), p.newState.GetData())

	// Update context: last 64 samples from input
	copy(p.context, inputData[len(inputData)-sileroContextSize:])

	return prob, nil
}

// ProcessCapture returns the frame unmodified. Voice activity is detected via
// Silero ONNX inference on resampled 16kHz audio.
func (p *Processor) ProcessCapture(frame []float32) ([]float32, bool, error) {
	if len(frame) == 0 {
		return frame, false, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, false, fmt.Errorf("apm: processor closed")
	}

	resampled := Resample(frame, SampleRate, SileroSampleRate)
	p.buf = append(p.buf, resampled...)

	for len(p.buf) >= SileroFrameSize {
		window := p.buf[:SileroFrameSize]

		prob, err := p.predict(window)
		if err != nil {
			p.logf("apm: silero predict error: %v", err)
		} else {
			p.lastVoice = prob >= p.threshold
		}

		remaining := len(p.buf) - SileroFrameSize
		copy(p.buf[:remaining], p.buf[SileroFrameSize:])
		p.buf = p.buf[:remaining]
	}

	return frame, p.lastVoice, nil
}

func (p *Processor) SetVADThreshold(threshold float64) {
	p.mu.Lock()
	p.threshold = float32(threshold)
	p.mu.Unlock()
	p.logf("apm: Silero VAD threshold set to %.4f", threshold)
}

func (p *Processor) VADThreshold() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return float64(p.threshold)
}

func (p *Processor) ProcessRender(frame []float32) error {
	return nil
}

func (p *Processor) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}
	p.closed = true

	if p.session != nil {
		p.session.Destroy()
	}
	if p.input != nil {
		p.input.Destroy()
	}
	if p.state != nil {
		p.state.Destroy()
	}
	if p.sr != nil {
		p.sr.Destroy()
	}
	if p.output != nil {
		p.output.Destroy()
	}
	if p.newState != nil {
		p.newState.Destroy()
	}
	p.buf = nil
	p.logf("apm: silero processor closed")
}

func downloadModel(path string, logf func(string, ...any)) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create model directory %s: %w", dir, err)
	}
	logf("apm: downloading Silero VAD model from %s", SileroModelURL)
	resp, err := http.Get(SileroModelURL) //nolint:gosec
	if err != nil {
		return fmt.Errorf("download model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download model: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create model file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(path)
		return fmt.Errorf("write model file: %w", err)
	}
	logf("apm: downloaded Silero VAD model to %s", path)
	return nil
}
