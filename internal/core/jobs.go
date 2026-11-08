package core

import (
	"fmt"
	"github.com/peragolabs/spendmux/internal/supervision"
	"github.com/peragolabs/spendmux/oss/memory"
	"sync"
	"time"
)

var (
	jobMu    sync.Mutex
	jobQueue []func()
	running  bool
)

// Enqueue runs fn asynchronously in a simple in-process FIFO queue.
func Enqueue(fn func()) {
	jobMu.Lock()
	jobQueue = append(jobQueue, fn)
	if !running {
		running = true
		go runJobs()
	}
	jobMu.Unlock()
}

func runJobs() {
	for {
		jobMu.Lock()
		if len(jobQueue) == 0 {
			running = false
			jobMu.Unlock()
			return
		}
		fn := jobQueue[0]
		jobQueue = jobQueue[1:]
		jobMu.Unlock()

		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("jobs: task panic: %v\n", r)
				}
			}()
			fn()
		}()
	}
}

// StartSessionCleanup deletes expired session rows and idle memory threads
// once an hour.
func StartSessionCleanup() {
	supervision.Every("session-cleanup", time.Hour, true, CleanupExpiredSessions)
	supervision.Every("memory-cleanup", time.Hour, true, memory.CleanupExpired)
}
