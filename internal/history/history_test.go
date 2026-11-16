package history

import (
	"reflect"
	"testing"
	"time"
)

func TestRingKeepsLastN(t *testing.T) {
	s := NewMemory(2)
	s.Add(1, 10, "a")
	s.Add(1, 10, "b")
	s.Add(1, 10, "c")
	s.Add(2, 10, "other chat")
	if got := s.Recent(1, 10); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("Recent = %v", got)
	}
}

func TestMarkFlaggedOnce(t *testing.T) {
	s := NewMemory(1)
	if !s.MarkFlagged(1, 5) {
		t.Fatal("first mark should be true")
	}
	if s.MarkFlagged(1, 5) {
		t.Fatal("second mark should be false")
	}
}

func TestFlaggedExpires(t *testing.T) {
	s := NewMemory(1)
	now := time.Unix(0, 0)
	s.now = func() time.Time { return now }
	s.MarkFlagged(1, 5)
	if !s.IsFlagged(1, 5) {
		t.Fatal("should be flagged")
	}
	now = now.Add(flaggedTTL + 2*time.Hour)
	s.MarkFlagged(1, 6) // triggers prune
	if s.IsFlagged(1, 5) {
		t.Fatal("old entry should be pruned")
	}
}

func TestMemoryCooldown(t *testing.T) {
	s := NewMemory(1)
	now := time.Unix(0, 0)
	s.now = func() time.Time { return now }
	if !s.Cooldown("x", 1, 2, time.Hour) {
		t.Fatal("first call should pass")
	}
	if s.Cooldown("x", 1, 2, time.Hour) {
		t.Fatal("second call within ttl should be blocked")
	}
	if !s.Cooldown("y", 1, 2, time.Hour) || !s.Cooldown("x", 1, 3, time.Hour) {
		t.Fatal("other name/user must be independent")
	}
	now = now.Add(2 * time.Hour)
	if !s.Cooldown("x", 1, 2, time.Hour) {
		t.Fatal("should pass after ttl")
	}
}
