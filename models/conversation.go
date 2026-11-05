// models/message.go
package models

import "github.com/google/uuid"

type Conversation struct {
	Base
	UserID   uuid.UUID `gorm:"type:uuid;index"`
	Title    string    `gorm:"size:255"`
	User     User      `gorm:"foreignKey:UserID"`
	Messages []Message `gorm:"foreignKey:ConversationID;order:created_at asc"`
}

type Message struct {
	Base
	ConversationID uuid.UUID `gorm:"type:uuid;index"`
	UserID         uuid.UUID `gorm:"type:uuid;index"`
	Role           string    `gorm:"size:20"` // "user" or "assistant"
	Content        string    `gorm:"type:text"`
	Model          string    `gorm:"size:100"`  // which model answered
	Cost           int       `gorm:"default:0"` // only user messages cost credits
}
