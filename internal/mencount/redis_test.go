package mencount

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStorePerPerson(t *testing.T) {
	m := miniredis.RunT(t)
	s := NewRedis(redis.NewClient(&redis.Options{Addr: m.Addr()}))

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

	top, err := s.Top(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Name != "Аня" || top[0].MenTotal() != 3 || top[1].WomenTotal() != 1 {
		t.Fatalf("Top = %+v", top)
	}
	if r := top[0].Ranked("m"); r[0].Name != "Дима" || r[0].Count != 2 {
		t.Fatalf("Ranked = %+v", r)
	}
	if other, _ := s.Top(-2); len(other) != 0 {
		t.Fatal("other chat should be empty")
	}
}
