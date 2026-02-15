#!/usr/bin/env bash
#
# Pre-stage voicedaemon project skeleton
# Run this BEFORE starting CC CLI session
#
# Usage: bash pre-stage-voicedaemon.sh
#
set -euo pipefail

PROJECT=~/git/voicedaemon
echo "Creating $PROJECT..."

mkdir -p "$PROJECT"/{cmd/voicedaemon,internal/{audio,stt,tts,daemon},scripts,docs/reports}
cd "$PROJECT"

# ─── go.mod ───
cat > go.mod << 'EOF'
module github.com/realnikolaj/voicedaemon

go 1.25
EOF


# ─── .golangci.yml ───
cat > .golangci.yml << 'EOF'
version: "2"
linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - ineffassign
    - gofmt
  settings:
    errcheck:
      exclude-functions:
        - (io.Closer).Close
EOF

# ─── Makefile ───
cat > Makefile << 'EOF'
.PHONY: build test lint check smoke

TAGS ?= noapm

build:
	go build -tags $(TAGS) ./cmd/voicedaemon/

test:
	go test -race -tags $(TAGS) ./...

lint:
	golangci-lint run

check: build test lint

smoke: build
	bash scripts/smoke-test.sh
EOF

# ─── Empty main so `go build` works immediately ───
cat > cmd/voicedaemon/main.go << 'EOF'
package main

func main() {
	// TODO: Kong CLI + daemon wiring (Task 14)
}
EOF

# ─── report-say.txt ───
echo 'Report ready: Sprint {sprint no.} complete' > report-say.txt

# ─── git init ───
git init
cat > .gitignore << 'EOF'
voicedaemon
*.exe
*.test
*.out
.DS_Store
EOF

git add -A
git commit -m "skeleton: project structure, go.mod, Makefile, golangci config"

# ─── Copy plan into project ───
if [ -f ~/Downloads/PLAN-voicedaemon-sprint1.md ]; then
    cp ~/Downloads/PLAN-voicedaemon-sprint1.md "$PROJECT/"
    echo "Copied PLAN file from Downloads"
elif [ -f ~/Desktop/PLAN-voicedaemon-sprint1.md ]; then
    cp ~/Desktop/PLAN-voicedaemon-sprint1.md "$PROJECT/"
    echo "Copied PLAN file from Desktop"
else
    echo "NOTE: Copy PLAN-voicedaemon-sprint1.md into $PROJECT/ manually"
fi

echo ""
echo "Done. Project at $PROJECT"
echo ""
echo "Verify cgo deps:"
echo "  pkg-config --cflags --libs webrtc-audio-processing"
echo "  pkg-config --cflags --libs portaudio-2.0"
echo ""
echo "If both pass, agent can build APM. If not, agent still works with -tags noapm."
