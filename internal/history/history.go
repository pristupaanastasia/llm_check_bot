// Package history keeps short-lived per-user state: recent "human" messages
// (the author's style sample), which messages the bot already answered, and
// reply cooldowns. Memory is the in-process implementation; Redis persists the
// same data across restarts.
package history

import (
	"fmt"
	"sync"
	"time"
)

// flaggedTTL is how long the bot remembers it already replied to a message.
// Edits normally arrive within hours, so older entries are dropped.
const flaggedTTL = 48 * time.Hour

// Store is what the bot needs from the history backend.
type Store interface {
	// Recent returns the user's recent messages in the chat, oldest first.
	Recent(chatID, userID int64) []string
	// Add remembers a message as a sample of the user's own style.
	Add(chatID, userID int64, text string)
	// IsFlagged reports whether the bot already replied to the message.
	IsFlagged(chatID int64, msgID int) bool
	// MarkFlagged records a reply and reports whether it was the first one.
	MarkFlagged(chatID int64, msgID int) bool
	// Cooldown reports whether an action for (chat, user) is allowed now and,
	// if so, blocks it for ttl. name separates independent cooldowns.
	Cooldown(name string, chatID, userID int64, ttl time.Duration) bool
}

type key struct {
	chatID int64
	userID int64
}

type msgKey struct {
	chatID int64
	msgID  int
}

// Memory is the in-process Store. Its data is lost on restart.
type Memory struct {
	mu        sync.Mutex
	size      int
	msgs      map[key][]string
	flagged   map[msgKey]time.Time
	lastPrune time.Time
	now       func() time.Time
	cooldowns map[string]time.Time
}

func NewMemory(size int) *Memory {
	return &Memory{size: size, msgs: map[key][]string{}, flagged: map[msgKey]time.Time{},
		cooldowns: map[string]time.Time{}, now: time.Now}
}

func (s *Memory) Cooldown(name string, chatID, userID int64, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := fmt.Sprintf("%s:%d:%d", name, chatID, userID)
	now := s.now()
	if until, ok := s.cooldowns[k]; ok && now.Before(until) {
		return false
	}
	s.cooldowns[k] = now.Add(ttl)
	return true
}

// Recent returns a copy of the user's recent messages in the chat, oldest first.
func (s *Memory) Recent(chatID, userID int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.msgs[key{chatID, userID}]...)
}

// Add remembers a message as a sample of the user's own style.
func (s *Memory) Add(chatID, userID int64, text string) {
	if s.size <= 0 || text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{chatID, userID}
	m := append(s.msgs[k], text)
	if len(m) > s.size {
		m = m[len(m)-s.size:]
	}
	s.msgs[k] = m
}

// IsFlagged reports whether the bot already replied to the message.
func (s *Memory) IsFlagged(chatID int64, msgID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.flagged[msgKey{chatID, msgID}]
	return ok
}

// MarkFlagged records that the bot replied to a message and reports whether it
// was the first time (so edits don't trigger a second reply).
func (s *Memory) MarkFlagged(chatID int64, msgID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Sub(s.lastPrune) > time.Hour {
		for k, t := range s.flagged {
			if now.Sub(t) > flaggedTTL {
				delete(s.flagged, k)
			}
		}
		s.lastPrune = now
	}
	k := msgKey{chatID, msgID}
	if _, ok := s.flagged[k]; ok {
		return false
	}
	s.flagged[k] = now
	return true
}
