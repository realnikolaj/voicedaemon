# TASK-VD-3.1: TTS Log Writer

## Status: Complete

## Scope
JSONL log file written after each TTS playback completes. New `--tts-log` CLI flag. Zero-cost when disabled.

## Files Modified
- `internal/tts/logwriter.go` — NEW: TTSLogWriter, TTSLogEntry, mutex-protected append
- `internal/tts/client.go` — Added ResolveVoiceModel() to expose resolved defaults
- `internal/tts/queue.go` — LogWriter field, timing, Write() after WaitUtterance
- `internal/daemon/daemon.go` — TTSLogPath config, writer create/close lifecycle
- `cmd/voicedaemon/main.go` — `--tts-log` Kong flag, env VOICEDAEMON_TTS_LOG

## Boundary Changes
- New CLI flag: `--tts-log <path>` (env: `VOICEDAEMON_TTS_LOG`)
- New public API: `tts.NewTTSLogWriter()`, `tts.TTSLogEntry`, `Client.ResolveVoiceModel()`
- Log format: JSONL with fields ts, text, voice, backend, model?, duration_ms?

## Verify
```bash
make check-noapm   # build + test + lint: 0 issues
voicedaemon --tts-log /tmp/tts.jsonl &
curl -s -X POST http://localhost:5111/speak -d '{"text":"test","voice":"glados"}'
sleep 3 && cat /tmp/tts.jsonl | python3 -m json.tool
```

## Notes
- Writer closes after queue drains in shutdown sequence
- duration_ms measures processJob start to WaitUtterance completion
