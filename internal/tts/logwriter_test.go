package tts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogWriterFiltering(t *testing.T) {
	tests := []struct {
		name        string
		entry       TTSLogEntry
		wantWritten bool
		wantContain string
	}{
		{
			name: "voice eponine is written",
			entry: TTSLogEntry{
				Text:    "test eponine",
				Voice:   "eponine",
				Backend: "speaches",
			},
			wantWritten: true,
		},
		{
			name: "voice glados is written",
			entry: TTSLogEntry{
				Text:    "test glados",
				Voice:   "glados",
				Backend: "speaches",
			},
			wantWritten: true,
		},
		{
			name: "voice alba is filtered out",
			entry: TTSLogEntry{
				Text:    "test alba",
				Voice:   "alba",
				Backend: "speaches",
			},
			wantWritten: false,
		},
		{
			name: "nolog skips entry",
			entry: TTSLogEntry{
				Text:    "secret",
				Voice:   "eponine",
				Backend: "speaches",
				NoLog:   true,
			},
			wantWritten: false,
		},
		{
			name: "backend pocket normalized to pockettts",
			entry: TTSLogEntry{
				Text:    "test pocket",
				Voice:   "eponine",
				Backend: "pocket",
			},
			wantWritten: true,
			wantContain: `"pockettts"`,
		},
		{
			name: "backend speaches unchanged",
			entry: TTSLogEntry{
				Text:    "test speaches",
				Voice:   "glados",
				Backend: "speaches",
			},
			wantWritten: true,
			wantContain: `"speaches"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "tts.log")

			w, err := NewTTSLogWriter(path)
			if err != nil {
				t.Fatalf("NewTTSLogWriter: %v", err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			}()

			if err := w.Write(tt.entry); err != nil {
				t.Fatalf("Write: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}

			content := strings.TrimSpace(string(data))
			hasContent := content != ""

			if hasContent != tt.wantWritten {
				t.Errorf("file has content = %v, want written = %v", hasContent, tt.wantWritten)
			}

			if tt.wantContain != "" && !strings.Contains(content, tt.wantContain) {
				t.Errorf("file content %q does not contain %q", content, tt.wantContain)
			}
		})
	}
}
