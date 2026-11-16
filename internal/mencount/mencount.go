// Package mencount keeps, per chat, how many times each member mentioned each
// man and woman by name in a romantic context. Counts live in a JSON file so
// they survive restarts.
package mencount

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Entry is one chat member's tallies.
type Entry struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	// Men and Women map a mentioned person's name to how many times this member
	// mentioned them romantically.
	Men   map[string]int `json:"men"`
	Women map[string]int `json:"women_by_name"`
}

// MenTotal is all romantic mentions of men by this member.
func (e Entry) MenTotal() int { return sum(e.Men) }

// WomenTotal is all romantic mentions of women by this member.
func (e Entry) WomenTotal() int { return sum(e.Women) }

// Total is all romantic mentions by this member.
func (e Entry) Total() int { return e.MenTotal() + e.WomenTotal() }

// Named is one mentioned person with a count.
type Named struct {
	Name  string
	Count int
}

// Ranked returns the member's mentioned people of one gender ("m" or "f"),
// most mentioned first.
func (e Entry) Ranked(gender string) []Named {
	m := e.Men
	if gender == "f" {
		m = e.Women
	}
	out := make([]Named, 0, len(m))
	for n, c := range m {
		out = append(out, Named{n, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sum(m map[string]int) int {
	t := 0
	for _, c := range m {
		t += c
	}
	return t
}

// Store is what the bot needs from the counter backend.
type Store interface {
	// Add records one romantic mention of person (gender "m" or "f") by the
	// user and returns the user's updated entry and the new count for person.
	Add(chatID, userID int64, userName, person, gender string) (Entry, int, error)
	// Top returns the chat's members with at least one mention, highest first.
	Top(chatID int64) ([]Entry, error)
}

// FileStore keeps per-chat tallies in memory and persists them to a JSON file.
type FileStore struct {
	mu   sync.Mutex
	path string
	// chats maps chat ID to user ID to entry (string keys for JSON).
	chats map[string]map[string]*Entry
}

// OpenFile loads the store from path; a missing file means an empty store.
func OpenFile(path string) (*FileStore, error) {
	s := &FileStore{path: path, chats: map[string]map[string]*Entry{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.chats); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileStore) Add(chatID, userID int64, userName, person, gender string) (Entry, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := strconv.FormatInt(chatID, 10)
	u := strconv.FormatInt(userID, 10)
	if s.chats[c] == nil {
		s.chats[c] = map[string]*Entry{}
	}
	e := s.chats[c][u]
	if e == nil {
		e = &Entry{UserID: userID}
		s.chats[c][u] = e
	}
	if e.Men == nil {
		e.Men = map[string]int{}
	}
	if e.Women == nil {
		e.Women = map[string]int{}
	}
	e.Name = userName // keep the latest display name

	m := e.Men
	if gender == "f" {
		m = e.Women
	}
	key := canonical(m, person)
	m[key]++
	return copyEntry(e), m[key], s.save()
}

// canonical returns the existing key matching person case-insensitively, so
// «дима» and «Дима» are one person.
func canonical(m map[string]int, person string) string {
	for k := range m {
		if strings.EqualFold(k, person) {
			return k
		}
	}
	return person
}

func copyEntry(e *Entry) Entry {
	c := Entry{UserID: e.UserID, Name: e.Name, Men: map[string]int{}, Women: map[string]int{}}
	for k, v := range e.Men {
		c.Men[k] = v
	}
	for k, v := range e.Women {
		c.Women[k] = v
	}
	return c
}

func (s *FileStore) Top(chatID int64) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.chats[strconv.FormatInt(chatID, 10)] {
		if e.Total() > 0 {
			out = append(out, copyEntry(e))
		}
	}
	sortEntries(out)
	return out, nil
}

func sortEntries(out []Entry) {
	sort.Slice(out, func(i, j int) bool {
		if ti, tj := out[i].Total(), out[j].Total(); ti != tj {
			return ti > tj
		}
		return out[i].Name < out[j].Name
	})
}

// save writes the file atomically (temp file + rename). Caller holds s.mu.
func (s *FileStore) save() error {
	data, err := json.MarshalIndent(s.chats, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
