package actor

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm/clause"

	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
)

var (
	defaultGateway *ActorGateway
	gatewayMu      sync.RWMutex
)

// SetDefaultGateway wires the process-wide gateway used by OSS packages.
func SetDefaultGateway(g *ActorGateway) {
	gatewayMu.Lock()
	defer gatewayMu.Unlock()
	defaultGateway = g
}

func DefaultGateway() *ActorGateway {
	gatewayMu.RLock()
	defer gatewayMu.RUnlock()
	return defaultGateway
}

// LogUsageEvent sends a usage row through the supervised DataActor.
// Falls back to direct DB write if the gateway is unavailable.
//
// The row id is fixed before sending, and both writes ignore a conflicting
// id: a send that times out after the actor already queued the row would
// otherwise record (and bill against the caps) the same request twice.
func LogUsageEvent(event models.UsageEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	g := DefaultGateway()
	if g != nil {
		if _, err := g.Send(TypeLogUsage, event, 3*time.Second); err == nil {
			return nil
		}
	}
	return insertUsageEvent(&event)
}

func insertUsageEvent(event *models.UsageEvent) error {
	return database.Get().Clauses(clause.OnConflict{DoNothing: true}).Create(event).Error
}
