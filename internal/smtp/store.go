package smtp

import (
	"sync"
)

const defaultMaxMessages = 500

// MailStore is a thread-safe, capped ring buffer of captured SMTP messages.
// When the store is at capacity, the oldest message is evicted to make room.
type MailStore struct {
	mu       sync.RWMutex
	messages []*CapturedMessage
	max      int
}

// NewMailStore returns a MailStore that keeps at most maxMessages messages.
// If maxMessages is ≤ 0, defaultMaxMessages is used.
func NewMailStore(maxMessages int) *MailStore {
	if maxMessages <= 0 {
		maxMessages = defaultMaxMessages
	}
	return &MailStore{
		messages: make([]*CapturedMessage, 0, min(maxMessages, 128)),
		max:      maxMessages,
	}
}

// Add saves a new message. If the store is full the oldest message is dropped.
func (s *MailStore) Add(m *CapturedMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) >= s.max {
		// Evict oldest (index 0).
		copy(s.messages, s.messages[1:])
		s.messages = s.messages[:len(s.messages)-1]
	}
	s.messages = append(s.messages, m)
}

// List returns all messages in reverse-chronological order (newest first).
func (s *MailStore) List() []*CapturedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*CapturedMessage, len(s.messages))
	for i, m := range s.messages {
		out[len(s.messages)-1-i] = m
	}
	return out
}

// Get returns the message with the given ID, or nil if not found.
func (s *MailStore) Get(id string) *CapturedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// Delete removes the message with the given ID.
// Returns true if a message was removed.
func (s *MailStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.messages {
		if m.ID == id {
			s.messages = append(s.messages[:i], s.messages[i+1:]...)
			return true
		}
	}
	return false
}

// Clear removes all messages.
func (s *MailStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = s.messages[:0]
}

// Len returns the current number of stored messages.
func (s *MailStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}
