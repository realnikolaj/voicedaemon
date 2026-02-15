package vdclient

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDaemon simulates the voicedaemon Unix socket server for testing.
type fakeDaemon struct {
	t        *testing.T
	listener net.Listener
	path     string
	handler  func(t *testing.T, conn net.Conn)
}

func newFakeDaemon(t *testing.T, handler func(t *testing.T, conn net.Conn)) *fakeDaemon {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	fd := &fakeDaemon{t: t, listener: ln, path: path, handler: handler}
	go fd.acceptLoop()
	return fd
}

func (f *fakeDaemon) acceptLoop() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handler(f.t, conn)
	}
}

func (f *fakeDaemon) close() {
	if err := f.listener.Close(); err != nil {
		f.t.Logf("fakeDaemon: listener close: %v", err)
	}
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		f.t.Logf("fakeDaemon: remove socket: %v", err)
	}
}

func writeConn(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Errorf("write to conn: %v", err)
	}
}

func TestDialAndStatus(t *testing.T) {
	fd := newFakeDaemon(t, func(t *testing.T, conn net.Conn) {
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("conn close: %v", err)
			}
		}()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := scanner.Text()
			switch cmd {
			case "status":
				writeConn(t, conn, "idle\n")
			}
		}
	})
	defer fd.close()

	session, err := Dial(fd.path)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	status, err := session.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status != "idle" {
		t.Errorf("status = %q, want %q", status, "idle")
	}
}

func TestStartStop(t *testing.T) {
	fd := newFakeDaemon(t, func(t *testing.T, conn net.Conn) {
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("conn close: %v", err)
			}
		}()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := scanner.Text()
			switch cmd {
			case "start":
				writeConn(t, conn, "started\n")
				// Simulate transcript pushes
				writeConn(t, conn, "transcript:hello world\n")
			case "stop":
				writeConn(t, conn, "hello world\n")
			}
		}
	})
	defer fd.close()

	session, err := Dial(fd.path)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	if err := session.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Drain transcript channel
	select {
	case text := <-session.Transcripts():
		if text != "hello world" {
			t.Errorf("transcript = %q, want %q", text, "hello world")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for transcript")
	}

	result, err := session.Stop()
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("stop result = %q, want %q", result, "hello world")
	}
}

func TestTranscriptChannel(t *testing.T) {
	fd := newFakeDaemon(t, func(t *testing.T, conn net.Conn) {
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("conn close: %v", err)
			}
		}()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := scanner.Text()
			switch cmd {
			case "start":
				writeConn(t, conn, "started\n")
				writeConn(t, conn, "transcript:one\n")
				writeConn(t, conn, "transcript:two\n")
				writeConn(t, conn, "transcript:three\n")
			case "stop":
				writeConn(t, conn, "one two three\n")
			}
		}
	})
	defer fd.close()

	session, err := Dial(fd.path)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	if err := session.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	var got []string
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		for text := range session.Transcripts() {
			mu.Lock()
			got = append(got, text)
			mu.Unlock()
		}
		close(done)
	}()

	// Give readLoop time to process transcript pushes
	time.Sleep(100 * time.Millisecond)

	result, err := session.Stop()
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	<-done

	mu.Lock()
	defer mu.Unlock()

	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("got %d transcripts, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("transcript[%d] = %q, want %q", i, got[i], w)
		}
	}
	if result != "one two three" {
		t.Errorf("stop result = %q, want %q", result, "one two three")
	}
}

func TestCancel(t *testing.T) {
	fd := newFakeDaemon(t, func(t *testing.T, conn net.Conn) {
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("conn close: %v", err)
			}
		}()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := scanner.Text()
			switch cmd {
			case "start":
				writeConn(t, conn, "started\n")
			case "cancel":
				writeConn(t, conn, "cancelled\n")
			}
		}
	})
	defer fd.close()

	session, err := Dial(fd.path)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	if err := session.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if err := session.Cancel(); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
}

func TestClose(t *testing.T) {
	fd := newFakeDaemon(t, func(t *testing.T, conn net.Conn) {
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("conn close: %v", err)
			}
		}()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := strings.TrimSpace(scanner.Text())
			switch cmd {
			case "status":
				writeConn(t, conn, "idle\n")
			}
		}
	})
	defer fd.close()

	session, err := Dial(fd.path)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}

	// Verify connection works
	status, err := session.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status != "idle" {
		t.Errorf("status = %q, want %q", status, "idle")
	}

	// Close and verify no error
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Double close should not error
	if err := session.Close(); err != nil {
		t.Fatalf("double Close() error: %v", err)
	}
}
