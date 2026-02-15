# Task: VD-2.2 — TTS Playback Pre-Buffer

## Status
Complete

## What Was Done

Added a per-utterance pre-buffer gate in `speaker.go` (Option A). The portaudio callback outputs silence until enough frames have accumulated in the queue, absorbing network jitter from HTTP chunked TTS responses.

### Mechanism

1. `BeginUtterance()` closes the gate (`gateOpen = false`)
2. `Feed()` enqueues resampled frames as before; after each enqueue, checks if queue depth has reached `preBufferFrames` (20 frames = 200ms at 48kHz) → opens the gate
3. `callback()` checks `gateOpen` before pulling from queue; outputs silence while gate is closed
4. `EndUtterance()` force-opens the gate so short utterances (< 200ms) still play

### Implementation Details

- Gate state uses `sync/atomic.Bool` — no mutex in the hot path (portaudio callback)
- `preBufferFrames = 20` (200ms) defined as a constant, easy to tune
- Queue capacity unchanged at 200 frames (2 seconds) — 10x the pre-buffer threshold
- No changes to `StopUtterance` — aborting drains the queue regardless of gate state

## Files Modified
- `internal/audio/speaker.go`:
  - Added `sync/atomic` import
  - Added `preBufferFrames = 20` constant
  - Added `gateOpen atomic.Bool` field to `Speaker`
  - `callback()`: early-return with silence when gate is closed
  - `BeginUtterance()`: closes gate before resetting state
  - `Feed()`: opens gate when queue depth reaches threshold
  - `EndUtterance()`: force-opens gate for short utterances

## Verification
```bash
make check        # real APM: build ✓, test ✓, lint ✓
make check-noapm  # stub:     build ✓, test ✓, lint ✓

# Manual test:
curl -X POST localhost:5111/speak -d '{"text":"This is a longer test sentence to verify that the pre-buffer prevents audio underruns. The playback should be smooth from start to finish without any crackles, speedup, or garbled audio at any point during this utterance."}'
```

## Notes
- The 200ms pre-buffer adds 200ms of latency to the start of each utterance. This is imperceptible for TTS (user expects a brief pause after sending text). Can be tuned down to 100ms if latency is a concern.
- The pre-buffer only affects the first 200ms. Once the gate opens, subsequent Feed() calls go straight through — no re-buffering mid-utterance.
- `len(s.queue)` in `Feed()` is an approximation (channel length can be slightly stale from another goroutine's perspective), but this is fine — worst case the gate opens 1 frame early or late.
