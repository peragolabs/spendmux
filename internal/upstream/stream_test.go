package upstream

import (
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdleBodyCancelsStalledStream(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	defer pw.Close()
	var cancelled atomic.Bool
	body := newIdleBody(pr, 50*time.Millisecond, func() { cancelled.Store(true); _ = pr.Close() })
	defer body.Close()

	go func() { _, _ = pw.Write([]byte("data: 1\n")) }()
	buf := make([]byte, 64)
	if _, err := body.Read(buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Nothing more arrives: the idle timer must cancel the request.
	deadline := time.Now().Add(2 * time.Second)
	for !cancelled.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !cancelled.Load() {
		t.Fatal("stalled stream was not cancelled")
	}
}

func TestIdleBodyKeepsActiveStreamAlive(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	var cancelled atomic.Bool
	body := newIdleBody(pr, 80*time.Millisecond, func() { cancelled.Store(true) })
	go func() {
		// Chunks every 30ms for well past the idle window.
		for i := 0; i < 10; i++ {
			_, _ = pw.Write([]byte("x"))
			time.Sleep(30 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	if _, err := io.ReadAll(body); err != nil {
		t.Fatalf("read: %v", err)
	}
	if cancelled.Load() {
		t.Fatal("an active stream was cancelled by the idle timer")
	}
	_ = body.Close()
	if !cancelled.Load() {
		t.Fatal("Close must release the request context")
	}
}
