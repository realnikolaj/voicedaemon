# Task: VD-2.7 — TTS logging + version flag

## Status
Complete

## What Was Done
- Task 1 (TTS logging): Already wired — `Logf: logf` is passed at daemon.go:108. No change needed.
- Task 2 (version flag): Added `kong.VersionFlag` field to CLI struct and `kong.Vars{"version": version}`. Default `var version = "0.2.6"`, overridable via `-ldflags "-X main.version=..."`.

## Files Modified
- `cmd/voicedaemon/main.go`: Added `var version`, `Version kong.VersionFlag` field, `kong.Vars{"version": version}`

## Verification
```bash
make check-noapm   # ✓ 0 issues
make check          # ✓ 0 issues
voicedaemon --version  # prints: 0.2.6
```

## Notes
- TTS queue logf was already wired in daemon.go — the nil/no-op path only applies if QueueConfig is constructed without Logf, which doesn't happen in production
- Version can be set at build time: `go build -ldflags "-X main.version=1.0.0" ./cmd/voicedaemon/`
