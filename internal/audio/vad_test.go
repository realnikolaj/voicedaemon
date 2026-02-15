package audio

import (
	"sync"
	"testing"
)

func TestVADStateMachine(t *testing.T) {
	tests := []struct {
		name       string
		steps      []struct{ hasVoice bool }
		wantStates []VadState
		wantCalls  int
	}{
		{
			name: "idle does nothing",
			steps: []struct{ hasVoice bool }{
				{true}, {false},
			},
			wantStates: []VadState{VadIdle, VadIdle},
			wantCalls:  0,
		},
		{
			name: "listening to recording on voice",
			steps: []struct{ hasVoice bool }{
				{false}, {false}, {true},
			},
			wantStates: []VadState{VadListening, VadListening, VadRecording},
			wantCalls:  0,
		},
		{
			name: "recording to processing on silence gap",
			steps: func() []struct{ hasVoice bool } {
				// 3 silence frames in listening, 1 voice frame, then 3 silence frames to trigger
				s := []struct{ hasVoice bool }{
					{false}, {false}, {true},
				}
				for range 3 {
					s = append(s, struct{ hasVoice bool }{false})
				}
				return s
			}(),
			wantStates: nil, // checked separately
			wantCalls:  1,
		},
		{
			name: "voice resets silence counter",
			steps: func() []struct{ hasVoice bool } {
				// Start listening, voice, 2 silence, voice, 2 silence — should NOT trigger
				s := []struct{ hasVoice bool }{
					{true}, {false}, {false}, {true}, {false}, {false},
				}
				return s
			}(),
			wantStates: nil,
			wantCalls:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			callCount := 0
			var lastAudio []float32

			cfg := VADConfig{
				PreBufferFrames:  2,
				SilenceGapFrames: 3,
			}

			vad := NewVADMachine(cfg, func(audio []float32) {
				mu.Lock()
				defer mu.Unlock()
				callCount++
				lastAudio = audio
			})

			if tt.name != "idle does nothing" {
				vad.Start()
			}

			frame := make([]float32, FrameSize)
			for i, step := range tt.steps {
				vad.ProcessFrame(frame, step.hasVoice)
				if tt.wantStates != nil && i < len(tt.wantStates) {
					got := vad.State()
					if got != tt.wantStates[i] {
						t.Errorf("step %d: state = %v, want %v", i, got, tt.wantStates[i])
					}
				}
			}

			mu.Lock()
			defer mu.Unlock()
			if callCount != tt.wantCalls {
				t.Errorf("onUtterance called %d times, want %d", callCount, tt.wantCalls)
			}

			if tt.wantCalls > 0 && len(lastAudio) == 0 {
				t.Error("onUtterance received empty audio")
			}
		})
	}
}

func TestVADStateString(t *testing.T) {
	tests := []struct {
		state VadState
		want  string
	}{
		{VadIdle, "idle"},
		{VadListening, "listening"},
		{VadRecording, "recording"},
		{VadProcessing, "processing"},
		{VadState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("VadState(%d).String() = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestVADPreBuffer(t *testing.T) {
	var receivedAudio []float32

	cfg := VADConfig{
		PreBufferFrames:  3,
		SilenceGapFrames: 2,
	}

	vad := NewVADMachine(cfg, func(audio []float32) {
		receivedAudio = audio
	})
	vad.Start()

	// Feed 3 silence frames (fills pre-buffer)
	for i := range 3 {
		frame := make([]float32, FrameSize)
		frame[0] = float32(i + 1) // tag each frame
		vad.ProcessFrame(frame, false)
	}

	// Voice frame triggers recording
	voiceFrame := make([]float32, FrameSize)
	voiceFrame[0] = 100
	vad.ProcessFrame(voiceFrame, true)

	// 2 silence frames triggers processing
	for range 2 {
		silenceFrame := make([]float32, FrameSize)
		vad.ProcessFrame(silenceFrame, false)
	}

	if receivedAudio == nil {
		t.Fatal("onUtterance was not called")
	}

	// Should have: 3 pre-buffer frames + 1 voice frame + 2 silence frames = 6 * FrameSize
	expectedLen := 6 * FrameSize
	if len(receivedAudio) != expectedLen {
		t.Errorf("audio length = %d, want %d", len(receivedAudio), expectedLen)
	}

	// First sample of first pre-buffer frame should be 1.0
	if receivedAudio[0] != 1.0 {
		t.Errorf("first pre-buffer frame tag = %f, want 1.0", receivedAudio[0])
	}
}

func TestVADStop(t *testing.T) {
	cfg := DefaultVADConfig()
	vad := NewVADMachine(cfg, nil)

	vad.Start()
	if vad.State() != VadListening {
		t.Errorf("after Start: state = %v, want listening", vad.State())
	}

	vad.Stop()
	if vad.State() != VadIdle {
		t.Errorf("after Stop: state = %v, want idle", vad.State())
	}
}
