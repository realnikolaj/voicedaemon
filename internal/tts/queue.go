package tts

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/realnikolaj/voicedaemon/internal/audio"
)

// Job represents a TTS job to be processed.
type Job struct {
	Text    string
	Backend Backend
	Opts    *StreamOpts
}

// Speaker is the interface the queue uses to control playback.
type Speaker interface {
	BeginUtterance()
	Feed(data []byte, srcRate int)
	EndUtterance()
	WaitUtterance(timeout time.Duration) error
	StopUtterance()
}

// RenderFeeder is called to feed audio through the APM render path for AEC.
type RenderFeeder interface {
	FeedRender(samples []float32) error
}

// Queue manages TTS jobs with strict FIFO serialization.
// Jobs are processed one at a time; use StopPlayback for explicit cancellation.
type Queue struct {
	client       *Client
	speaker      Speaker
	renderFeeder RenderFeeder
	logWriter    *TTSLogWriter
	logf         func(string, ...any)

	mu       sync.Mutex
	jobs     chan Job
	cancel   context.CancelFunc
	ctx      context.Context
	wg       sync.WaitGroup
	running  bool
	depth    int
	curAbort context.CancelFunc
}

// QueueConfig holds configuration for the TTS queue.
type QueueConfig struct {
	Client       *Client
	Speaker      Speaker
	RenderFeeder RenderFeeder
	LogWriter    *TTSLogWriter
	Logf         func(string, ...any)
}

// NewQueue creates a new TTS job queue.
func NewQueue(cfg QueueConfig) *Queue {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	return &Queue{
		client:       cfg.Client,
		speaker:      cfg.Speaker,
		renderFeeder: cfg.RenderFeeder,
		logWriter:    cfg.LogWriter,
		logf:         logf,
		jobs:         make(chan Job, 16),
	}
}

// Start begins the queue worker goroutine.
func (q *Queue) Start() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.running {
		return
	}

	q.ctx, q.cancel = context.WithCancel(context.Background())
	q.running = true

	q.wg.Add(1)
	go q.worker()

	q.logf("tts-queue: started")
}

// Enqueue appends a job to the FIFO queue.
// The worker processes jobs strictly one at a time.
func (q *Queue) Enqueue(job Job) {
	q.mu.Lock()
	q.depth++
	q.mu.Unlock()

	q.jobs <- job
	q.logf("tts-queue: enqueued %q via %s (depth=%d)", job.Text, job.Backend, q.Depth())
}

// Depth returns the current queue depth.
func (q *Queue) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depth
}

// StopPlayback drains all pending jobs and aborts current playback.
func (q *Queue) StopPlayback() {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.jobs) > 0 {
		<-q.jobs
		q.depth--
	}

	if q.curAbort != nil {
		q.curAbort()
		q.speaker.StopUtterance()
	}

	q.logf("tts-queue: playback stopped")
}

// Stop shuts down the queue worker.
func (q *Queue) Stop() {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		return
	}
	q.cancel()
	q.mu.Unlock()

	q.wg.Wait()

	q.mu.Lock()
	q.running = false
	q.mu.Unlock()

	q.logf("tts-queue: stopped")
}

func (q *Queue) worker() {
	defer q.wg.Done()

	for {
		select {
		case <-q.ctx.Done():
			return
		case job, ok := <-q.jobs:
			if !ok {
				return
			}
			q.processJob(job)
		}
	}
}

func (q *Queue) processJob(job Job) {
	jobCtx, jobCancel := context.WithCancel(q.ctx)
	started := time.Now()

	q.mu.Lock()
	q.curAbort = jobCancel
	q.mu.Unlock()

	label := job.Text
	if len(label) > 30 {
		label = label[:30]
	}
	q.logf("tts-queue: processing job: %q via %s", label, job.Backend)

	defer func() {
		jobCancel()
		q.mu.Lock()
		q.curAbort = nil
		q.depth--
		q.mu.Unlock()
		q.logf("tts-queue: job complete: %q", label)
	}()

	chunks, sampleRate, err := q.client.Stream(jobCtx, job.Text, job.Backend, job.Opts)
	if err != nil {
		q.logf("tts-queue: stream error: %v", err)
		return
	}

	q.speaker.BeginUtterance()

	for chunk := range chunks {
		select {
		case <-jobCtx.Done():
			q.logf("tts-queue: job aborted")
			q.speaker.StopUtterance()
			return
		default:
		}

		// Feed to speaker for playback
		q.speaker.Feed(chunk, sampleRate)

		// Feed render path for AEC if available
		if q.renderFeeder != nil {
			if err := q.feedRender(chunk, sampleRate); err != nil {
				q.logf("tts-queue: render feed error: %v", err)
			}
		}
	}

	q.speaker.EndUtterance()
	if err := q.speaker.WaitUtterance(30 * time.Second); err != nil {
		q.logf("tts-queue: wait utterance: %v", err)
	}

	// Write TTS log entry after confirmed playback
	if q.logWriter != nil {
		model, voice := q.client.ResolveVoiceModel(job.Backend, job.Opts)
		entry := TTSLogEntry{
			Text:       job.Text,
			Voice:      voice,
			Backend:    string(job.Backend),
			Model:      model,
			DurationMs: time.Since(started).Milliseconds(),
		}
		if err := q.logWriter.Write(entry); err != nil {
			q.logf("tts-queue: log write error: %v", err)
		}
	}
}

// feedRender converts PCM s16le bytes to float32, resamples to 48kHz,
// and feeds through the AEC render path.
func (q *Queue) feedRender(data []byte, sampleRate int) error {
	numSamples := len(data) / 2
	if numSamples == 0 {
		return nil
	}

	samples := make([]float32, numSamples)
	for i := range numSamples {
		lo := data[i*2]
		hi := data[i*2+1]
		s := int16(uint16(lo) | uint16(hi)<<8)
		samples[i] = float32(s) / 32768.0
	}

	// APM render path expects 48kHz; resample if TTS source rate differs.
	if sampleRate != audio.SampleRate {
		samples = audio.Resample(samples, sampleRate, audio.SampleRate)
	}

	if err := q.renderFeeder.FeedRender(samples); err != nil {
		return fmt.Errorf("tts-queue: feed render: %w", err)
	}
	return nil
}
