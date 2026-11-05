package supervision

import (
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// Go runs fn as a supervised worker: a panic is recovered and logged, and the
// worker is restarted with exponential backoff (100 ms → 30 s) instead of
// taking the process down — an unrecovered panic in any goroutine would.
// fn should loop until stop is closed and then return; a clean return ends
// the worker. Returns a stop function.
func Go(name string, fn func(stop <-chan struct{})) (stop func()) {
	ch := make(chan struct{})
	var once sync.Once
	go func() {
		delay := 100 * time.Millisecond
		for attempt := 0; ; attempt++ {
			ok := runOnce(name, fn, ch)
			select {
			case <-ch:
				return
			default:
			}
			if ok {
				return // clean exit
			}
			slog.Warn("supervised worker restarting", "worker", name, "attempt", attempt+1, "delay", delay)
			select {
			case <-time.After(delay):
			case <-ch:
				return
			}
			if delay < 30*time.Second {
				delay *= 2
			}
		}
	}()
	return func() { once.Do(func() { close(ch) }) }
}

func runOnce(name string, fn func(<-chan struct{}), stop <-chan struct{}) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("supervised worker panicked", "worker", name, "panic", r, "stack", string(debug.Stack()))
			ok = false
		}
	}()
	fn(stop)
	return true
}

// Every runs fn on a fixed interval under supervision. A panic in one tick is
// logged and the ticker resumes after backoff. runNow controls an immediate first run.
func Every(name string, interval time.Duration, runNow bool, fn func()) (stop func()) {
	return Go(name, func(stop <-chan struct{}) {
		if runNow {
			fn()
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fn()
			case <-stop:
				return
			}
		}
	})
}
