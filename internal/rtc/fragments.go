package rtc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// envelope is the wrapper the Speaches WebRTC server puts around every
// data channel message. Small messages arrive as full_message; larger ones
// (>900 bytes base64) are split into partial_message fragments.
type envelope struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	Data           string `json:"data"`            // base64-encoded JSON payload
	FragmentIndex  int    `json:"fragment_index"`  // 0-based, partial only
	TotalFragments int    `json:"total_fragments"` // partial only
}

// partialBuf tracks in-flight fragmented messages.
type partialBuf struct {
	fragments []string // indexed by fragment_index
	received  int
	total     int
}

// reassembler decodes the Speaches data channel framing protocol and emits
// complete JSON payloads. It is not safe for concurrent use — the caller
// must serialise calls or use a single goroutine.
type reassembler struct {
	pending map[string]*partialBuf
}

func newReassembler() *reassembler {
	return &reassembler{pending: make(map[string]*partialBuf)}
}

// feed accepts one raw data channel message and returns the decoded JSON
// payload if a complete event is ready, or nil if more fragments are needed.
// An error is returned only for unrecoverable parse failures.
func (r *reassembler) feed(raw []byte) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// Legacy format: raw JSON event without wrapper — pass straight through.
		return raw, nil
	}

	switch env.Type {
	case "full_message":
		return base64.StdEncoding.DecodeString(env.Data)

	case "partial_message":
		buf, ok := r.pending[env.ID]
		if !ok {
			buf = &partialBuf{
				fragments: make([]string, env.TotalFragments),
				total:     env.TotalFragments,
			}
			r.pending[env.ID] = buf
		}
		if env.FragmentIndex < 0 || env.FragmentIndex >= len(buf.fragments) {
			return nil, fmt.Errorf("rtc: fragment index %d out of range %d", env.FragmentIndex, len(buf.fragments))
		}
		buf.fragments[env.FragmentIndex] = env.Data
		buf.received++

		if buf.received < buf.total {
			return nil, nil // still waiting
		}

		// All fragments received — concatenate data fields then base64-decode.
		delete(r.pending, env.ID)
		var combined string
		for _, f := range buf.fragments {
			combined += f
		}
		return base64.StdEncoding.DecodeString(combined)

	default:
		// Unknown wrapper type or unwrapped legacy event.
		return raw, nil
	}
}

// transcriptEvent carries just the fields we care about from
// conversation.item.input_audio_transcription.completed.
type transcriptEvent struct {
	Type        string `json:"type"`
	Transcript  string `json:"transcript"`  // top-level shorthand (some builds)
	Item        *struct {
		Content []struct {
			Transcript string `json:"transcript"`
		} `json:"content"`
	} `json:"item"`
}

// extractTranscript parses a decoded realtime event and returns the
// transcript text if this is a completed transcription event, or "" otherwise.
func extractTranscript(payload []byte) string {
	var ev transcriptEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return ""
	}
	if ev.Type != "conversation.item.input_audio_transcription.completed" {
		return ""
	}
	// Try top-level transcript field first (Speaches shorthand).
	if ev.Transcript != "" {
		return ev.Transcript
	}
	// Fall back to item.content[0].transcript.
	if ev.Item != nil && len(ev.Item.Content) > 0 {
		return ev.Item.Content[0].Transcript
	}
	return ""
}
