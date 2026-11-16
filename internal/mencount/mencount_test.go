package mencount

import (
	"path/filepath"
	"testing"
)

func TestStorePerPerson(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	s, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	add := func(user int64, name, person, g string) int {
		t.Helper()
		_, n, err := s.Add(-1, user, name, person, g)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	add(10, "Аня", "Дима", "m")
	add(10, "Аня", "Петя", "m")
	if n := add(10, "Аня", "дима", "m"); n != 2 {
		t.Fatalf("Дима count = %d, want 2 (case-insensitive)", n)
	}
	add(20, "Вася", "Маша", "f")

	s2, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	top, _ := s2.Top(-1)
	if len(top) != 2 || top[0].Name != "Аня" || top[0].MenTotal() != 3 || top[1].WomenTotal() != 1 {
		t.Fatalf("Top = %+v", top)
	}
	r := top[0].Ranked("m")
	if r[0].Name != "Дима" || r[0].Count != 2 || r[1].Name != "Петя" {
		t.Fatalf("Ranked = %+v", r)
	}
	if other, _ := s2.Top(-2); len(other) != 0 {
		t.Fatal("other chat should be empty")
	}
}
