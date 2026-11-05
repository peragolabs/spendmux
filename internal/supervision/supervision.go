package supervision

import (
	"time"

	"github.com/vrannang1/spendmux/internal/actor"
	"github.com/vrannang1/spendmux/internal/supervisor"
)

var (
	registry *actor.ActorRegistry
	gateway  *actor.ActorGateway
	sup      *supervisor.Supervisor
)

// Start boots the supervised DataActor.
func Start() {
	registry = actor.NewActorRegistry()
	gateway = actor.NewActorGateway(registry)
	sup = supervisor.NewSupervisor(registry, func() actor.Actor {
		return actor.NewDataActor()
	})
	sup.Start()
}

// Stop shuts down the supervisor.
func Stop() {
	if sup != nil {
		sup.Stop()
	}
}

// Gateway returns the actor gateway for HTTP handlers.
func Gateway() *actor.ActorGateway {
	return gateway
}

// Stats returns restart count and supervisor status.
func Stats() (int, string) {
	if sup == nil {
		return 0, "stopped"
	}
	return sup.GetStats()
}

// Send is a convenience wrapper with a default timeout.
func Send(msgType string, payload any) error {
	_, err := gateway.Send(msgType, payload, 5*time.Second)
	return err
}
