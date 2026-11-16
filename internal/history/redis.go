package history

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"
)

// opTimeout bounds every Redis call so a stuck Redis can't hang the bot.
const opTimeout = 3 * time.Second

// Redis is a Store backed by Redis, so state survives bot restarts.
// On Redis errors it logs and fails open (the bot keeps working without
// history rather than stopping).
type Redis struct {
	c    *redis.Client
	size int
}

func NewRedis(c *redis.Client, size int) *Redis {
	return &Redis{c: c, size: size}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

func histKey(chatID, userID int64) string    { return fmt.Sprintf("hist:%d:%d", chatID, userID) }
func flagKey(chatID int64, msgID int) string { return fmt.Sprintf("flag:%d:%d", chatID, msgID) }

func (r *Redis) Recent(chatID, userID int64) []string {
	c, cancel := ctx()
	defer cancel()
	msgs, err := r.c.LRange(c, histKey(chatID, userID), 0, -1).Result()
	if err != nil {
		log.Printf("redis history read: %v", err)
		return nil
	}
	slices.Reverse(msgs) // stored newest first
	return msgs
}

func (r *Redis) Add(chatID, userID int64, text string) {
	if r.size <= 0 || text == "" {
		return
	}
	c, cancel := ctx()
	defer cancel()
	k := histKey(chatID, userID)
	_, err := r.c.TxPipelined(c, func(p redis.Pipeliner) error {
		p.LPush(c, k, text)
		p.LTrim(c, k, 0, int64(r.size-1))
		return nil
	})
	if err != nil {
		log.Printf("redis history write: %v", err)
	}
}

func (r *Redis) IsFlagged(chatID int64, msgID int) bool {
	c, cancel := ctx()
	defer cancel()
	n, err := r.c.Exists(c, flagKey(chatID, msgID)).Result()
	if err != nil {
		log.Printf("redis flag read: %v", err)
		return false
	}
	return n > 0
}

func (r *Redis) MarkFlagged(chatID int64, msgID int) bool {
	c, cancel := ctx()
	defer cancel()
	ok, err := r.c.SetNX(c, flagKey(chatID, msgID), 1, flaggedTTL).Result()
	if err != nil {
		log.Printf("redis flag write: %v", err)
		return true // better a possible duplicate reply than none
	}
	return ok
}

// PausedKey is set by scripts/thermal-guard.sh while the Mac is too hot.
const PausedKey = "llm:paused"

func (r *Redis) ModelPaused() bool {
	c, cancel := ctx()
	defer cancel()
	n, err := r.c.Exists(c, PausedKey).Result()
	if err != nil {
		log.Printf("redis pause read: %v", err)
		return false
	}
	return n > 0
}

func (r *Redis) Cooldown(name string, chatID, userID int64, ttl time.Duration) bool {
	c, cancel := ctx()
	defer cancel()
	ok, err := r.c.SetNX(c, fmt.Sprintf("cooldown:%s:%d:%d", name, chatID, userID), 1, ttl).Result()
	if err != nil {
		log.Printf("redis cooldown: %v", err)
		return true
	}
	return ok
}
