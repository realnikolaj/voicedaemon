package daemon

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func tempSocket(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "voice-daemon-test-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func dialSocket(t *testing.T, path string) net.Conn {
	t.Helper()
	var conn net.Conn
	var err error
	for range 50 {
		conn, err = net.Dial("unix", path)
		if err == nil {
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Logf("conn close: %v", err)
				}
			})
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("failed to connect to %s: %v", path, err)
	return nil
}

func readLine(t *testing.T, conn net.Conn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(string(buf[:n]))
}

func startSocketServer(t *testing.T, path string, srv *SocketServer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSocketStatus(t *testing.T) {
	path := tempSocket(t)
	t.Cleanup(func() { _ = os.Remove(path) })

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)
	srv.SetCallbacks(nil, nil, nil, func() string { return "idle" })

	startSocketServer(t, path, srv)

	conn := dialSocket(t, path)

	if _, err := conn.Write([]byte("status\n")); err != nil {
		t.Fatal(err)
	}

	got := readLine(t, conn)
	if got != "idle" {
		t.Errorf("status = %q, want %q", got, "idle")
	}
}

func TestSocketStartStop(t *testing.T) {
	path := tempSocket(t)
	t.Cleanup(func() { _ = os.Remove(path) })

	var started, stopped bool

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)
	srv.SetCallbacks(
		func() { started = true },
		func() string { stopped = true; return "hello world" },
		nil,
		func() string { return "idle" },
	)

	startSocketServer(t, path, srv)

	conn := dialSocket(t, path)

	// Start
	if _, err := conn.Write([]byte("start\n")); err != nil {
		t.Fatal(err)
	}
	got := readLine(t, conn)
	if got != "started" {
		t.Errorf("start response = %q, want %q", got, "started")
	}
	if !started {
		t.Error("onStart not called")
	}

	// Stop
	if _, err := conn.Write([]byte("stop\n")); err != nil {
		t.Fatal(err)
	}
	got = readLine(t, conn)
	if got != "hello world" {
		t.Errorf("stop response = %q, want %q", got, "hello world")
	}
	if !stopped {
		t.Error("onStop not called")
	}
}

func TestSocketCancel(t *testing.T) {
	path := tempSocket(t)
	t.Cleanup(func() { _ = os.Remove(path) })

	var cancelled bool

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)
	srv.SetCallbacks(nil, nil, func() { cancelled = true }, nil)

	startSocketServer(t, path, srv)

	conn := dialSocket(t, path)

	if _, err := conn.Write([]byte("cancel\n")); err != nil {
		t.Fatal(err)
	}

	got := readLine(t, conn)
	if got != "cancelled" {
		t.Errorf("cancel response = %q, want %q", got, "cancelled")
	}
	if !cancelled {
		t.Error("onCancel not called")
	}
}

func TestSocketTranscriptPush(t *testing.T) {
	path := tempSocket(t)
	t.Cleanup(func() { _ = os.Remove(path) })

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)
	srv.SetCallbacks(func() {}, nil, nil, nil)

	startSocketServer(t, path, srv)

	conn := dialSocket(t, path)

	// Start session
	if _, err := conn.Write([]byte("start\n")); err != nil {
		t.Fatal(err)
	}
	_ = readLine(t, conn) // "started"

	// Push transcript
	srv.PushTranscript("hello")

	got := readLine(t, conn)
	if got != "transcript:hello" {
		t.Errorf("push = %q, want %q", got, "transcript:hello")
	}
}

func TestSocketCleanup(t *testing.T) {
	path := tempSocket(t)

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Socket file should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("socket file does not exist after Start")
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}

	// Socket file should be cleaned up
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("socket file still exists after Close")
	}
}

func TestSocketStaleCleanup(t *testing.T) {
	path := tempSocket(t)

	// Create a stale socket file
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should succeed despite stale file
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start with stale socket: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSocketAccumulatedTranscripts(t *testing.T) {
	path := tempSocket(t)
	t.Cleanup(func() { _ = os.Remove(path) })

	cfg := SocketConfig{Path: path}
	srv := NewSocketServer(cfg)
	srv.SetCallbacks(
		func() {},
		func() string { return "" }, // Return empty to use accumulated transcripts
		nil,
		nil,
	)

	startSocketServer(t, path, srv)

	conn := dialSocket(t, path)

	// Start
	if _, err := conn.Write([]byte("start\n")); err != nil {
		t.Fatal(err)
	}
	_ = readLine(t, conn)

	// Push two transcripts
	srv.PushTranscript("hello")
	_ = readLine(t, conn) // transcript:hello

	srv.PushTranscript("world")
	_ = readLine(t, conn) // transcript:world

	// Stop should return accumulated
	if _, err := conn.Write([]byte("stop\n")); err != nil {
		t.Fatal(err)
	}
	got := readLine(t, conn)
	if got != "hello world" {
		t.Errorf("stop = %q, want %q", got, "hello world")
	}
}
