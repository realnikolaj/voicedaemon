package rtc

import (
	"fmt"
	"time"

	"github.com/pion/webrtc/v3/pkg/media"
	"gopkg.in/hraban/opus.v2"
)

const (
	opusSampleRate  = 48000
	opusChannels    = 2   // stereo — required by aiortc on the Speaches server
	opusFrameSamples = 480 // 10ms at 48kHz per channel (matches portaudio FrameSize)
	opusMaxBytes    = 4096
	opusFrameDur    = 10 * time.Millisecond
)

// opusEncoder wraps the CGO libopus encoder. It accepts mono float32 PCM
// from portaudio, duplicates it to stereo, encodes to Opus, and writes
// to a pion TrackLocalStaticSample.
type opusEncoder struct {
	enc     *opus.Encoder
	stereo  [opusFrameSamples * opusChannels]int16
	outBuf  [opusMaxBytes]byte
}

func newOpusEncoder() (*opusEncoder, error) {
	enc, err := opus.NewEncoder(opusSampleRate, opusChannels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	// Optimize for low-latency voice.
	enc.SetBitrate(32000)
	enc.SetComplexity(5)
	return &opusEncoder{enc: enc}, nil
}

// writeTo encodes a mono float32 frame (480 samples / 10ms at 48kHz) and
// pushes it to the WebRTC audio track.
func (e *opusEncoder) writeTo(mono []float32, track interface{ WriteSample(media.Sample) error }) error {
	if len(mono) != opusFrameSamples {
		return fmt.Errorf("opus: expected %d mono samples, got %d", opusFrameSamples, len(mono))
	}

	// Convert float32 mono → int16 stereo interleaved [L,R,L,R,...]
	for i, s := range mono {
		// Clamp to [-1, 1]
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		v := int16(s * 32767)
		e.stereo[i*2] = v
		e.stereo[i*2+1] = v
	}

	n, err := e.enc.Encode(e.stereo[:opusFrameSamples*opusChannels], e.outBuf[:])
	if err != nil {
		return fmt.Errorf("opus encode: %w", err)
	}

	return track.WriteSample(media.Sample{
		Data:     e.outBuf[:n],
		Duration: opusFrameDur,
	})
}

func (e *opusEncoder) close() {
	// opus.Encoder is GC'd — nothing to free explicitly.
}
