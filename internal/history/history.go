// Package history keeps recent "human" messages of each watched user, so the
// detector can compare a new message with the author's usual writing style.
package history

import "sync"

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
	mu      sync.Mutex
	size    int
	msgs    map[key][]string
	flagged map[msgKey]bool
}

func New(size int) *Store {
	return &Store{size: size, msgs: map[key][]string{}, flagged: map[msgKey]bool{}}
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

// MarkFlagged records that the bot already replied to a message and reports
// whether it was the first time (so edits don't trigger a second reply).
func (s *Store) MarkFlagged(chatID int64, msgID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := msgKey{chatID, msgID}
	if s.flagged[k] {
		return false
	}
	s.flagged[k] = true
	return true
}
