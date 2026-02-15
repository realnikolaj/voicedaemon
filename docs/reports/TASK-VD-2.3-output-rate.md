# Task: VD-2.3 — Match Python daemon output rate

## Status
Complete

## What Was Done
- Created build-tagged output rate constants so noapm builds run portaudio output at 24kHz (matching Kokoro native rate) while real APM builds stay at 48kHz for AEC symmetry
- Updated speaker to use `OutputSampleRate` / `OutputFrameSize` instead of hardcoded 48kHz values
- Added resampling bypass in `Feed()` — when source rate matches output rate, raw PCM passes straight through with zero resampling

## Files Modified
- `internal/audio/rate.go` (new): `//go:build !noapm` → `OutputSampleRate = 48000`, `OutputFrameSize = 480`
- `internal/audio/rate_noapm.go` (new): `//go:build noapm` → `OutputSampleRate = 24000`, `OutputFrameSize = 240`
- `internal/audio/speaker.go`: `DefaultSpeakerConfig()` uses `OutputSampleRate`/`OutputFrameSize`; `Feed()` skips `ResampleS16LE` when `srcRate == int(s.sampleRate)`

## Verification
```bash
make check-noapm   # ✓ build + test + lint (0 issues)
make check          # ✓ build + test + lint (0 issues)

# Runtime validation (requires running Speaches):
# 1. Build and run: go build -tags noapm ./cmd/voicedaemon/ && ./voicedaemon
# 2. curl -X POST localhost:5111/speak -d '{"text":"This is a long test to verify clean playback without any garbling or crackling throughout the entire utterance"}'
# Expected: clean 24kHz playback, no resampling in logs
```

## Notes
- The noapm speaker now opens portaudio at 24kHz with 240-sample frames (10ms), eliminating the 24→48kHz resampling path that caused cumulative artifacts
- Real APM builds are completely unaffected — they still run at 48kHz/480 for AEC symmetry
- Piper/GLaDOS at 22050 Hz will still resample to 24kHz (noapm) — a much smaller ratio than 22050→48000, fewer artifacts
- `FeedFloat32` (AEC render path) is unchanged since it's only used in real APM builds where output is already 48kHz
