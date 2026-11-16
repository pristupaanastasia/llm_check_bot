package history

import (
	"reflect"
	"testing"
)

func TestRingKeepsLastN(t *testing.T) {
	s := New(2)
	s.Add(1, 10, "a")
	s.Add(1, 10, "b")
	s.Add(1, 10, "c")
	s.Add(2, 10, "other chat")
	if got := s.Recent(1, 10); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("Recent = %v", got)
	}
}

func TestMarkFlaggedOnce(t *testing.T) {
	s := New(1)
	if !s.MarkFlagged(1, 5) {
		t.Fatal("first mark should be true")
	}
	if s.MarkFlagged(1, 5) {
		t.Fatal("second mark should be false")
	}
}
