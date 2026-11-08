package supervisor

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/peragolabs/spendmux/internal/actor"
)

type RestartPolicy string

const (
	RestartAlways     RestartPolicy = "always"
	RestartNever      RestartPolicy = "never"
	RestartMaxRetries RestartPolicy = "max_retries"
)

type Supervisor struct {
	registry *actor.ActorRegistry
	factory  func() actor.Actor

	policy      RestartPolicy
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration

	attempts int
	restarts int
	status   string

	ctx     context.Context
	cancel  context.CancelFunc
	mailbox chan actor.Message
	mu      sync.RWMutex
}

func NewSupervisor(registry *actor.ActorRegistry, factory func() actor.Actor) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		registry:    registry,
		factory:     factory,
		policy:      RestartMaxRetries,
		maxAttempts: 5,
		baseDelay:   200 * time.Millisecond,
		maxDelay:    5 * time.Second,
		status:      "stopped",
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (s *Supervisor) Start() {
	s.mu.Lock()
	s.status = "running"
	s.mu.Unlock()
	go s.monitor()
}

func (s *Supervisor) Stop() {
	s.cancel()
	s.mu.Lock()
	s.status = "stopped"
	s.registry.Unregister()
	if s.mailbox != nil {
		close(s.mailbox)
		s.mailbox = nil
	}
	s.mu.Unlock()
}

func (s *Supervisor) GetStats() (int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.restarts, s.status
}

func (s *Supervisor) closeCurrentMailbox(mailbox chan actor.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mailbox == mailbox {
		close(mailbox)
		s.mailbox = nil
	}
}

func (s *Supervisor) monitor() {
	for {
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		mailbox := make(chan actor.Message, 100)
		s.mailbox = mailbox
		s.status = "running"
		s.registry.Register(mailbox)
		s.mu.Unlock()

		err := s.runActor(mailbox)
		s.registry.Unregister()
		s.closeCurrentMailbox(mailbox)

		select {
		case <-s.ctx.Done():
			return
		default:
		}

		if err != nil {
			log.Printf("[Supervisor] child exited: %v", err)
		}

		if !s.shouldRetry() {
			s.mu.Lock()
			s.status = "failed"
			s.mu.Unlock()
			return
		}

		s.mu.Lock()
		s.attempts++
		s.restarts++
		s.status = "restarting"
		delay := s.calculateDelayLocked()
		s.mu.Unlock()

		log.Printf("[Supervisor] restarting in %v (attempt %d)", delay, s.attempts)
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (s *Supervisor) runActor(mailbox chan actor.Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("actor panic: %v", r)
		}
	}()
	child := s.factory()
	return child.Run(mailbox)
}

func (s *Supervisor) shouldRetry() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch s.policy {
	case RestartAlways:
		return true
	case RestartNever:
		return false
	case RestartMaxRetries:
		return s.attempts < s.maxAttempts
	default:
		return false
	}
}

func (s *Supervisor) calculateDelayLocked() time.Duration {
	exponent := float64(s.attempts - 1)
	delay := time.Duration(float64(s.baseDelay) * math.Pow(2, exponent))
	if delay > s.maxDelay {
		return s.maxDelay
	}
	return delay
}
