# Task: VD-2.4 — Speaker queue capacity

## Status
Complete

## What Was Done
- Increased `speakerChunkQueueCap` from 200 to 2000 (200ms → 20s of buffered audio)
- Changed `Feed()` enqueue from silent drop to blocking with 500ms timeout + log on drop

## Files Modified
- `internal/audio/speaker.go`: `speakerChunkQueueCap` 200→2000; `Feed()` enqueue uses `time.After(500ms)` with log instead of `default` drop

## Verification
```bash
make check-noapm   # ✓ 0 issues
make check          # ✓ 0 issues
```

## Notes
- 2000 frames × 10ms = 20s — covers any Kokoro burst delivery
- 500ms timeout prevents indefinite blocking while giving the portaudio callback time to drain during normal playback
- If "queue full, dropped frame" appears in logs, something else is wrong (playback stalled)
