# Task: VD-2.1 — TTS Playback Sample Rate Hotfix

## Status
Complete

## Investigation Findings

The suspected root cause (no resampling between TTS output and portaudio) was **incorrect**. The resampling path already existed:

```
queue.processJob() → speaker.Feed(chunk, sampleRate=24000)
  → speaker.Feed() → ResampleS16LE(data, 24000, 48000)
    → Resample() → linear interpolation, output is 2x length
      → enqueue int16 chunks at 48kHz
```

Two actual bugs were found and fixed:

### Bug 1: HTTP chunk byte misalignment (client.go)
`resp.Body.Read()` can return any number of bytes, including odd counts. PCM s16le requires 2-byte alignment. When a read returns an odd byte count, the last byte of the chunk is orphaned. The next chunk starts with a shifted alignment, causing every subsequent sample to be assembled from the wrong byte pair. This produces exactly the described symptom: "starts playing correctly then accelerates into garbled noise."

### Bug 2: AEC render feed not resampled (queue.go)
`feedRender()` converted TTS PCM bytes (24kHz/22050Hz) to float32 but fed them directly to `pipeline.FeedRender()` without resampling to 48kHz. The APM render path expects 48kHz 10ms frames. Feeding wrong-rate audio corrupts the AEC state, which then corrupts `ProcessCapture` results on the mic path — degrading noise suppression and voice detection quality.

## What Was Done

### Fix 1: PCM byte alignment guard (client.go)
Added carry-byte mechanism to the streaming goroutine. When `Read()` returns an odd byte count, the trailing byte is saved and prepended to the next read. All chunks sent to the channel are guaranteed to have even length, maintaining s16le sample alignment across HTTP chunk boundaries.

### Fix 2: AEC render resampling (queue.go)
Added `audio.Resample()` call in `feedRender()` to convert TTS float32 samples from their native rate to 48kHz before feeding the APM render path. Uses existing `audio.Resample` (linear interpolation) — handles both Kokoro 24kHz (2x) and Piper 22050Hz (non-integer ratio).

## Files Modified
- `internal/tts/client.go`: Added carry-byte mechanism for s16le alignment in streaming goroutine
- `internal/tts/queue.go`: Added `audio` import, resample to 48kHz in `feedRender()`

## Files NOT Modified
- `internal/audio/speaker.go` — resampling already correct here
- `internal/audio/resample.go` — existing resampler is reused
- `internal/audio/pipeline.go` — no changes needed
- `internal/daemon/daemon.go` — no changes needed

## Verification
```bash
make check        # real APM: build ✓, test ✓, lint ✓
make check-noapm  # stub:     build ✓, test ✓, lint ✓

# Manual validation:
curl -X POST localhost:5111/speak -d '{"text":"Testing one two three four five six seven eight nine ten"}'
# Should play at normal speed, complete without crash
```

## Notes
- The `tts` package now imports `internal/audio` for `Resample()` and `SampleRate` constant. This is intentional — CLAUDE.md says "keep resampling in internal/audio/resample.go, don't scatter."
- No circular dependency: `daemon` → `audio` + `tts`, `tts` → `audio`, `audio` → (no internal deps).
- The odd-byte chunk bug is non-deterministic — it depends on TCP segmentation and HTTP chunked transfer encoding. May not reproduce on every run, which explains "starts correctly" behavior.
