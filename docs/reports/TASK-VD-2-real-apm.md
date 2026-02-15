# Task: VD-2 — Real WebRTC Audio Processing

## Status
Complete

## What Was Done

### Task 1: Copy C wrapper files
- Copied `apm_wrapper.h` and `apm_wrapper.cpp` from `reference/` into `internal/capm/`
- Files placed in isolated `internal/capm/` package (not `internal/audio/`) to avoid Go's "C++ source files not allowed when not using cgo" error during noapm builds

### Task 2: Rewrite apm.go with direct cgo
- Replaced phantom `github.com/jfreymuth/go-webrtc-apm` import with `internal/capm` package
- `internal/capm/capm.go`: thin cgo bindings using `#cgo pkg-config: webrtc-audio-processing-2`
- `internal/audio/apm.go`: Go-level `Processor` wrapping `capm.Handle`
- Uses `apm_create_with_aec()` for AEC3 support
- `ProcessCapture` copies frame before C call and returns the copy (original untouched)
- Same `Processor` interface as noapm stub — drop-in replacement

### Task 3: APM tests (`!noapm` build tag)
- `internal/audio/apm_test.go` with `//go:build !noapm`
- Table-driven tests: create/close, ProcessCapture valid/invalid, ProcessRender valid/invalid, closed processor errors, double-close safety, copy-not-original verification
- All 6 test functions, 11 subtests pass with race detector

### Task 4: Zero changes to consumers
- `pipeline.go`: ZERO changes (verified via `git diff`)
- `daemon.go`: ZERO changes
- `tts/queue.go`: ZERO changes
- Real APM drops in without touching any consumer code

### Task 5: Makefile dual-build
- `make check` → real APM (build + test + lint, no tags)
- `make check-noapm` → stub (build + test + lint with `-tags noapm`)
- Individual targets: `build`, `build-noapm`, `test`, `test-noapm`, `lint`, `lint-noapm`
- Removed hardcoded `TAGS ?= noapm` default
- Updated `.golangci.yml` to remove forced `noapm` build tag

### Task 6: Remove phantom go-webrtc-apm references
- `go.mod` had NO phantom `jfreymuth/go-webrtc-apm` entry (the import only existed in the build-excluded `apm.go`)
- Old `apm.go` with the phantom import has been fully replaced
- `reference/osog-apm.go` tagged with `//go:build ignore` to prevent compilation

### Task 7: README + CLAUDE.md updates
- README: added build prerequisites (`brew install webrtc-audio-processing`), APM build modes table, dual-build commands, attribution section
- CLAUDE.md: updated build environment (webrtc IS installed), quality gates (dual `make check`/`make check-noapm`), package boundaries (added `internal/capm/`), commands section, rule 9 (C++ isolation)

## Architecture Decision: internal/capm/
The C++ wrapper files (`apm_wrapper.h`, `apm_wrapper.cpp`) are isolated in `internal/capm/` rather than placed directly in `internal/audio/`. This is required because Go rejects C++ source files in any package where cgo is not active. Since `internal/audio/apm.go` has `//go:build !noapm`, building with the `noapm` tag excludes it, disabling cgo for that package — causing Go to error on any .cpp files present. By isolating cgo+C++ in `internal/capm/`, the package is only compiled when imported (which only happens from the `!noapm` code path).

## Files Modified
- `internal/capm/capm.go`: NEW — cgo bindings to WebRTC APM C wrapper
- `internal/capm/apm_wrapper.h`: COPIED from reference/
- `internal/capm/apm_wrapper.cpp`: COPIED from reference/
- `internal/audio/apm.go`: REWRITTEN — cgo via internal/capm, replaces jfreymuth import
- `internal/audio/apm_test.go`: NEW — table-driven APM tests (!noapm)
- `reference/osog-apm.go`: MODIFIED — build tag changed to `ignore`
- `Makefile`: REWRITTEN — dual-build targets
- `.golangci.yml`: MODIFIED — removed forced noapm build tag
- `README.md`: REWRITTEN — build prereqs, APM modes, attribution
- `CLAUDE.md`: MODIFIED — build environment, quality gates, commands, package boundaries

## Files NOT Modified (verified)
- `internal/audio/pipeline.go`
- `internal/daemon/daemon.go`
- `internal/tts/queue.go`
- `internal/audio/apm_noapm.go`
- `go.mod` / `go.sum`

## Verification
```bash
# Both builds pass all quality gates:
make check        # real APM: build ✓, test ✓ (11 APM subtests), lint ✓
make check-noapm  # stub:     build ✓, test ✓, lint ✓

# Prerequisite:
pkg-config --cflags --libs webrtc-audio-processing-2  # ✓
```

## Notes
- go.mod unchanged — no new external dependencies added (capm is internal)
- The `FramesPerBuffer` constant in osog-apm.go doesn't exist in this project; our code uses `FrameSize` from `internal/audio/audio.go`
- Smoke test (`make smoke`) still uses noapm build since it needs to run without audio hardware access
