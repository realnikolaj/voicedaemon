//go:build !noapm && !silero

package audio

import "testing"

func TestAPMProcessorCreateClose(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "create and close"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(PipelineConfig{Logf: t.Logf})
			if err != nil {
				t.Fatalf("NewProcessor() error: %v", err)
			}
			p.Close()
		})
	}
}

func TestAPMProcessCapture(t *testing.T) {
	tests := []struct {
		name      string
		frameSize int
		wantErr   bool
	}{
		{name: "valid frame", frameSize: FrameSize, wantErr: false},
		{name: "wrong frame size", frameSize: 100, wantErr: true},
		{name: "empty frame", frameSize: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(PipelineConfig{Logf: t.Logf})
			if err != nil {
				t.Fatalf("NewProcessor() error: %v", err)
			}
			defer p.Close()

			frame := make([]float32, tt.frameSize)
			_, _, err = p.ProcessCapture(frame)
			if (err != nil) != tt.wantErr {
				t.Errorf("ProcessCapture() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAPMProcessCaptureReturnsNewSlice(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "returns copy not original"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(PipelineConfig{Logf: t.Logf})
			if err != nil {
				t.Fatalf("NewProcessor() error: %v", err)
			}
			defer p.Close()

			original := make([]float32, FrameSize)
			for i := range original {
				original[i] = 0.5
			}

			clean, _, err := p.ProcessCapture(original)
			if err != nil {
				t.Fatalf("ProcessCapture() error: %v", err)
			}

			if &clean[0] == &original[0] {
				t.Error("ProcessCapture() returned same slice, expected copy")
			}
		})
	}
}

func TestAPMProcessRender(t *testing.T) {
	tests := []struct {
		name      string
		frameSize int
		wantErr   bool
	}{
		{name: "valid render frame", frameSize: FrameSize, wantErr: false},
		{name: "wrong render frame size", frameSize: 100, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(PipelineConfig{Logf: t.Logf})
			if err != nil {
				t.Fatalf("NewProcessor() error: %v", err)
			}
			defer p.Close()

			frame := make([]float32, tt.frameSize)
			err = p.ProcessRender(frame)
			if (err != nil) != tt.wantErr {
				t.Errorf("ProcessRender() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAPMProcessClosedProcessor(t *testing.T) {
	tests := []struct {
		name string
		op   string
	}{
		{name: "capture after close", op: "capture"},
		{name: "render after close", op: "render"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(PipelineConfig{Logf: t.Logf})
			if err != nil {
				t.Fatalf("NewProcessor() error: %v", err)
			}
			p.Close()

			frame := make([]float32, FrameSize)
			switch tt.op {
			case "capture":
				_, _, err = p.ProcessCapture(frame)
			case "render":
				err = p.ProcessRender(frame)
			}
			if err == nil {
				t.Errorf("expected error after Close(), got nil")
			}
		})
	}
}

func TestAPMDoubleClose(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "double close is safe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewProcessor(PipelineConfig{Logf: t.Logf})
			if err != nil {
				t.Fatalf("NewProcessor() error: %v", err)
			}
			p.Close()
			p.Close() // Should not panic
		})
	}
}
