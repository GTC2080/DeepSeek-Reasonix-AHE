// Package agent wires a Provider, a tool Registry, and a Session into the
// harness loop that drives a coding task to completion.
package agent

import "reasonix/internal/provider"

// Session holds the conversation history for one task.
type Session struct {
	Messages []provider.Message
}

// NewSession initializes a session with an optional system prompt.
func NewSession(system string) *Session {
	s := &Session{}
	if system != "" {
		s.Messages = append(s.Messages, provider.Message{Role: provider.RoleSystem, Content: system})
	}
	return s
}

// Add appends a message.
func (s *Session) Add(m provider.Message) {
	s.Messages = append(s.Messages, m)
}
