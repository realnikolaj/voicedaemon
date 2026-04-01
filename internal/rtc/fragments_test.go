package rtc

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func makeEnvelope(typ, id, data string, fragIdx, total int) []byte {
	env := envelope{
		Type:           typ,
		ID:             id,
		Data:           data,
		FragmentIndex:  fragIdx,
		TotalFragments: total,
	}
	b, _ := json.Marshal(env)
	return b
}

func TestFullMessage(t *testing.T) {
	payload := `{"type":"session.created"}`
	msg := makeEnvelope("full_message", "abc", b64(payload), 0, 0)

	r := newReassembler()
	got, err := r.feed(msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestPartialMessage(t *testing.T) {
	payload := `{"type":"conversation.item.input_audio_transcription.completed","transcript":"hello world"}`
	encoded := b64(payload)

	// Split into 3 fragments
	size := len(encoded) / 3
	frags := []string{encoded[:size], encoded[size : size*2], encoded[size*2:]}

	r := newReassembler()

	// First two fragments return nil
	for i := 0; i < 2; i++ {
		got, err := r.feed(makeEnvelope("partial_message", "msg1", frags[i], i, 3))
		if err != nil {
			t.Fatalf("fragment %d error: %v", i, err)
		}
		if got != nil {
			t.Errorf("fragment %d: expected nil, got %q", i, got)
		}
	}

	// Third fragment completes the message
	got, err := r.feed(makeEnvelope("partial_message", "msg1", frags[2], 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestLegacyUnwrapped(t *testing.T) {
	payload := []byte(`{"type":"session.created","session":{}}`)
	r := newReassembler()
	got, err := r.feed(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestExtractTranscript(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "completed with top-level transcript",
			payload: `{"type":"conversation.item.input_audio_transcription.completed","transcript":"hello world"}`,
			want:    "hello world",
		},
		{
			name:    "completed with item.content",
			payload: `{"type":"conversation.item.input_audio_transcription.completed","item":{"content":[{"transcript":"hi there"}]}}`,
			want:    "hi there",
		},
		{
			name:    "wrong event type",
			payload: `{"type":"session.created"}`,
			want:    "",
		},
		{
			name:    "empty transcript",
			payload: `{"type":"conversation.item.input_audio_transcription.completed","transcript":""}`,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTranscript([]byte(tc.payload))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMultipleInterleaved(t *testing.T) {
	// Two messages in flight simultaneously
	p1 := b64(`{"type":"event1"}`)
	p2 := b64(`{"type":"event2"}`)

	r := newReassembler()

	// Interleave fragments from two different messages
	r.feed(makeEnvelope("partial_message", "id1", p1[:len(p1)/2], 0, 2))
	r.feed(makeEnvelope("partial_message", "id2", p2[:len(p2)/2], 0, 2))

	got1, _ := r.feed(makeEnvelope("partial_message", "id1", p1[len(p1)/2:], 1, 2))
	got2, _ := r.feed(makeEnvelope("partial_message", "id2", p2[len(p2)/2:], 1, 2))

	if string(got1) != `{"type":"event1"}` {
		t.Errorf("id1: got %q, want event1", got1)
	}
	if string(got2) != `{"type":"event2"}` {
		t.Errorf("id2: got %q, want event2", got2)
	}
}
