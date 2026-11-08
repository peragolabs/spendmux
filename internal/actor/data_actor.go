package actor

import (
	"fmt"
	"log"

	"github.com/peragolabs/spendmux/models"
)

// DataActor runs DB side-effects (usage logging) in an isolated mailbox loop.
// Panics restart via the supervisor without taking down the HTTP process.
type DataActor struct{}

func NewDataActor() *DataActor {
	return &DataActor{}
}

func (a *DataActor) Run(mailbox chan Message) error {
	log.Println("[DataActor] Event loop started")
	for msg := range mailbox {
		a.handleMessage(msg)
	}
	log.Println("[DataActor] Mailbox closed, event loop exiting")
	return nil
}

func (a *DataActor) handleMessage(msg Message) {
	var resp Response

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[DataActor] CRITICAL panic: %v", r)
			resp.Err = fmt.Errorf("data actor crashed: %v", r)
			if msg.ResponseChan != nil {
				select {
				case msg.ResponseChan <- resp:
				default:
				}
			}
			panic(r) // re-panic for supervisor restart
		}
	}()

	switch msg.Type {
	case TypeDebugCrash:
		log.Println("[DataActor] Simulated crash")
		panic("data actor crash")

	case TypeLogUsage:
		event, ok := msg.Payload.(models.UsageEvent)
		if !ok {
			resp.Err = fmt.Errorf("invalid LogUsage payload")
			break
		}
		resp.Err = insertUsageEvent(&event)

	default:
		resp.Err = fmt.Errorf("unknown message type: %s", msg.Type)
	}

	if msg.ResponseChan != nil {
		select {
		case msg.ResponseChan <- resp:
		default:
		}
	}
}
