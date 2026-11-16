package history

import (
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T, size int) (*Redis, *miniredis.Miniredis) {
	t.Helper()
	m := miniredis.RunT(t)
	return NewRedis(redis.NewClient(&redis.Options{Addr: m.Addr()}), size), m
}

func TestRedisHistory(t *testing.T) {
	r, _ := newTestRedis(t, 2)
	r.Add(1, 10, "a")
	r.Add(1, 10, "b")
	r.Add(1, 10, "c")
	r.Add(2, 10, "other chat")
	if got := r.Recent(1, 10); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("Recent = %v", got)
	}
}

func TestRedisFlagsAndCooldown(t *testing.T) {
	r, m := newTestRedis(t, 1)
	if r.IsFlagged(1, 5) || !r.MarkFlagged(1, 5) || r.MarkFlagged(1, 5) || !r.IsFlagged(1, 5) {
		t.Fatal("flag sequence wrong")
	}
	m.FastForward(flaggedTTL + time.Minute)
	if r.IsFlagged(1, 5) {
		t.Fatal("flag should expire")
	}

	if !r.Cooldown("support", 1, 2, time.Hour) || r.Cooldown("support", 1, 2, time.Hour) {
		t.Fatal("cooldown should block second call")
	}
	m.FastForward(2 * time.Hour)
	if !r.Cooldown("support", 1, 2, time.Hour) {
		t.Fatal("cooldown should expire")
	}
}

func TestRedisModelPaused(t *testing.T) {
	r, m := newTestRedis(t, 1)
	if r.ModelPaused() {
		t.Fatal("not paused by default")
	}
	m.Set(PausedKey, "2")
	if !r.ModelPaused() {
		t.Fatal("should be paused")
	}
}
