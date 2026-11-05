package actor

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrActorUnavailable = errors.New("supervised actor is currently unavailable")
	ErrRequestTimeout   = errors.New("request to actor timed out")
)

// ActorRegistry holds the mailbox channel for the currently active instance of an actor.
// It is updated by the supervisor when an actor restarts.
type ActorRegistry struct {
	mu      sync.RWMutex
	mailbox chan Message
}

func NewActorRegistry() *ActorRegistry {
	return &ActorRegistry{}
}

// Register stores the active actor's channel.
func (r *ActorRegistry) Register(mailbox chan Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mailbox = mailbox
}

// Unregister clears the actor's channel.
func (r *ActorRegistry) Unregister() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mailbox = nil
}

// GetMailbox retrieves the active channel.
func (r *ActorRegistry) GetMailbox() chan Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mailbox
}

// ActorGateway is the entry point for other packages to communicate with supervised actors.
type ActorGateway struct {
	registry *ActorRegistry
}

func NewActorGateway(registry *ActorRegistry) *ActorGateway {
	return &ActorGateway{registry: registry}
}

// Send sends a message to the actor and waits for a response or timeout.
func (g *ActorGateway) Send(msgType string, payload interface{}, timeout time.Duration) (data interface{}, err error) {
	mailbox := g.registry.GetMailbox()
	if mailbox == nil {
		return nil, ErrActorUnavailable
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	responseChan := make(chan Response, 1)
	msg := Message{
		Type:         msgType,
		Payload:      payload,
		ResponseChan: responseChan,
	}

	// Sending on a mailbox closed during restart/shutdown panics; treat as unavailable.
	defer func() {
		if r := recover(); r != nil {
			data, err = nil, ErrActorUnavailable
		}
	}()

	select {
	case mailbox <- msg:
	case <-time.After(timeout):
		return nil, ErrRequestTimeout
	}

	select {
	case res := <-responseChan:
		return res.Data, res.Err
	case <-time.After(timeout):
		return nil, ErrRequestTimeout
	}
}
