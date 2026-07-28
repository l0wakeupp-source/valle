package swarm

import (
	"encoding/json"
	"time"
)

// MessageType classifies the purpose of an inter-agent message.
type MessageType string

const (
	MsgTask      MessageType = "task"      // assign work to an agent
	MsgResponse  MessageType = "response"  // reply to a question
	MsgBroadcast MessageType = "broadcast"  // notify all agents
	MsgResult    MessageType = "result"    // post a completed finding
	MsgQuestion  MessageType = "question"  // ask another agent something
)

// Message is the envelope for agent-to-agent communication.
type Message struct {
	ID      string         `json:"id"`
	From    string         `json:"from"`
	To      string         `json:"to"`      // "*" = broadcast
	Type    MessageType    `json:"type"`
	Content string         `json:"content"`
	Meta    map[string]any `json:"meta,omitempty"`
	Time    time.Time      `json:"time"`
}

// NewMessage creates a message with an auto-set timestamp.
func NewMessage(from, to string, mt MessageType, content string) Message {
	return Message{
		From:    from,
		To:      to,
		Type:    mt,
		Content: content,
		Time:    time.Now(),
	}
}

// Marshal serializes the message to JSON bytes.
func (m Message) Marshal() []byte {
	b, _ := json.Marshal(m)
	return b
}

// UnmarshalMessage parses a message from JSON bytes.
func UnmarshalMessage(b []byte) (Message, error) {
	var m Message
	err := json.Unmarshal(b, &m)
	return m, err
}

// IsBroadcast returns true if the message targets all agents.
func (m Message) IsBroadcast() bool {
	return m.To == "*" || m.To == ""
}
