package supervision

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestGoRestartsAfterPanic(t *testing.T) {
	var runs int32
	stop := Go("panicky", func(stop <-chan struct{}) {
		if atomic.AddInt32(&runs, 1) == 1 {
			panic("boom")
		}
		<-stop
	})
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&runs) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&runs) < 2 {
		t.Fatal("worker was not restarted after panic")
	}
}

func TestEveryStops(t *testing.T) {
	var ticks int32
	stop := Every("ticker", 10*time.Millisecond, true, func() { atomic.AddInt32(&ticks, 1) })
	time.Sleep(60 * time.Millisecond)
	stop()
	n := atomic.LoadInt32(&ticks)
	time.Sleep(40 * time.Millisecond)
	if n < 2 || atomic.LoadInt32(&ticks) > n+1 {
		t.Fatalf("expected ticks then stop; got %d then %d", n, atomic.LoadInt32(&ticks))
	}
}
