# Task: VD-2.5 — Fix premature utterance-done signal

## Status
Complete

## What Was Done
- Removed premature done signal from `EndUtterance()` — it was firing when `len(queue) == 0` at the moment of the call, racing with the portaudio callback draining frames
- Done signal now comes exclusively from the portaudio callback (line 111-118), which fires only when: `eos == true` AND `len(queue) == 0` AND `playing == true` — i.e., after the last frame has actually been played
- This matches the Python daemon pattern where the done event is set inside the callback, not the feeding side

## Root Cause
`EndUtterance()` checked `len(s.queue) == 0` and immediately signaled done. With Kokoro burst delivery, the callback could drain the queue faster than expected, or a momentary empty read between callback pulls caused a false-positive. `WaitUtterance()` then returned immediately, and the next job started while audio was still playing.

## Files Modified
- `internal/audio/speaker.go`: Removed `if len(s.queue) == 0 { ... done signal ... }` from `EndUtterance()`; added queue depth to log message

## Verification
```bash
make check-noapm   # ✓ 0 issues
make check          # ✓ 0 issues
```

## Notes
- Empty utterances (0 frames fed) still work: `EndUtterance()` forces gate open, next callback sees eos + empty queue and fires done
- The callback's done path is mutex-protected and checks all three conditions atomically
