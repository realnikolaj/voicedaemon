package tts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TTSLogEntry is a single JSON-lines log record written after TTS playback completes.
type TTSLogEntry struct {
	Timestamp  string `json:"ts"`
	Text       string `json:"text"`
	Voice      string `json:"voice"`
	Backend    string `json:"backend"`
	Model      string `json:"model,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// TTSLogWriter appends JSON-lines entries to a file after each TTS utterance completes.
type TTSLogWriter struct {
	mu   sync.Mutex
	file *os.File
}

// NewTTSLogWriter creates a new log writer that appends to the given path.
// Parent directories are created if they do not exist.
func NewTTSLogWriter(path string) (*TTSLogWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("tts-logwriter: create dir %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("tts-logwriter: open %s: %w", path, err)
	}

	return &TTSLogWriter{file: f}, nil
}

// Write marshals the entry to JSON and appends it as a single line.
func (w *TTSLogWriter) Write(entry TTSLogEntry) error {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("tts-logwriter: marshal: %w", err)
	}

	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("tts-logwriter: write: %w", err)
	}

	return nil
}

// Close flushes and closes the underlying file.
func (w *TTSLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	if err := w.file.Close(); err != nil {
		return fmt.Errorf("tts-logwriter: close: %w", err)
	}
	w.file = nil

	return nil
}
