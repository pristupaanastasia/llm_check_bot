// Package history keeps recent "human" messages of each watched user, so the
// detector can compare a new message with the author's usual writing style.
package history

import (
	"sync"
	"time"
)

// flaggedTTL is how long the bot remembers it already replied to a message.
// Edits normally arrive within hours, so older entries are dropped.
const flaggedTTL = 48 * time.Hour

type key struct {
	chatID int64
	userID int64
}

type msgKey struct {
	chatID int64
	msgID  int
}

// Store is an in-memory, per-chat-per-user ring of recent messages.
// It is lost on restart; that is fine — the baseline refills quickly.
type Store struct {
	mu        sync.Mutex
	size      int
	msgs      map[key][]string
	flagged   map[msgKey]time.Time
	lastPrune time.Time
	now       func() time.Time
}

func New(size int) *Store {
	return &Store{size: size, msgs: map[key][]string{}, flagged: map[msgKey]time.Time{}, now: time.Now}
}

// Recent returns a copy of the user's recent messages in the chat, oldest first.
func (s *Store) Recent(chatID, userID int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.msgs[key{chatID, userID}]...)
}

// Add remembers a message as a sample of the user's own style.
func (s *Store) Add(chatID, userID int64, text string) {
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
func (s *Store) IsFlagged(chatID int64, msgID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.flagged[msgKey{chatID, msgID}]
	return ok
}

// MarkFlagged records that the bot replied to a message and reports whether it
// was the first time (so edits don't trigger a second reply).
func (s *Store) MarkFlagged(chatID int64, msgID int) bool {
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
