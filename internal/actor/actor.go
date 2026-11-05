package actor

// Message is the container passed between HTTP handlers and supervised actors.
type Message struct {
	Type         string
	Payload      interface{}
	ResponseChan chan Response
}

// Response is the synchronous answer sent back by the actor.
type Response struct {
	Data interface{}
	Err  error
}

// Actor is a supervised processing unit.
type Actor interface {
	Run(mailbox chan Message) error
}

// Message types for the DB / usage actor.
const (
	TypeLogUsage   = "LogUsage"
	TypeDebugCrash = "DebugCrash"
)
