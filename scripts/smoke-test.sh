#!/usr/bin/env bash
set -euo pipefail

BINARY="./voicedaemon"
SOCKET="/tmp/voice-daemon-smoke-test.sock"
PORT=15111
PID=""

cleanup() {
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
    fi
    rm -f "$SOCKET"
}
trap cleanup EXIT

echo "=== voicedaemon smoke test ==="

# 1. Build
echo "[1/6] Building binary..."
go build -tags noapm -o "$BINARY" ./cmd/voicedaemon/
echo "  OK: binary built"

# 2. Start daemon in background
echo "[2/6] Starting daemon (port=$PORT, socket=$SOCKET)..."
"$BINARY" --port "$PORT" --socket-path "$SOCKET" --debug &
PID=$!
sleep 2

if ! kill -0 "$PID" 2>/dev/null; then
    echo "  FAIL: daemon exited prematurely"
    exit 1
fi
echo "  OK: daemon running (pid=$PID)"

# 3. Test Unix socket — status
echo "[3/6] Testing Unix socket status..."
STATUS=$(python3 -c "
import socket, sys
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect('$SOCKET')
s.sendall(b'status\n')
data = s.recv(1024).decode().strip()
s.close()
print(data)
" 2>/dev/null || echo "FAIL")
if [ "$STATUS" = "idle" ]; then
    echo "  OK: socket status = idle"
else
    echo "  FAIL: expected 'idle', got '$STATUS'"
    exit 1
fi

# 4. Test HTTP health
echo "[4/6] Testing HTTP /health..."
HEALTH=$(curl -s "http://localhost:$PORT/health")
HEALTH_STATUS=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])" 2>/dev/null || echo "FAIL")
if [ "$HEALTH_STATUS" = "ok" ]; then
    echo "  OK: health status = ok"
else
    echo "  FAIL: expected 'ok', got '$HEALTH_STATUS'"
    echo "  Response: $HEALTH"
    exit 1
fi

# 5. Test HTTP /speak (will fail to reach Speaches but should queue)
echo "[5/6] Testing HTTP /speak..."
SPEAK=$(curl -s -X POST "http://localhost:$PORT/speak" \
    -H 'Content-Type: application/json' \
    -d '{"text":"smoke test"}')
SPEAK_STATUS=$(echo "$SPEAK" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])" 2>/dev/null || echo "FAIL")
if [ "$SPEAK_STATUS" = "queued" ]; then
    echo "  OK: speak status = queued"
else
    echo "  FAIL: expected 'queued', got '$SPEAK_STATUS'"
    echo "  Response: $SPEAK"
    exit 1
fi

# 6. Clean shutdown
echo "[6/6] Testing clean shutdown..."
kill "$PID"
wait "$PID" 2>/dev/null || true
PID=""

if [ -e "$SOCKET" ]; then
    echo "  FAIL: stale socket file remains after shutdown"
    exit 1
fi
echo "  OK: clean shutdown (no stale socket)"

echo ""
echo "=== ALL SMOKE TESTS PASSED ==="
