package mencount

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore keeps tallies in Redis:
//
//	men:<chat>:users            set of user IDs with mentions
//	men:<chat>:names            hash user ID -> display name
//	men:<chat>:<user>:m / :f    hash person name -> count
type RedisStore struct {
	c *redis.Client
}

func NewRedis(c *redis.Client) *RedisStore { return &RedisStore{c: c} }

const opTimeout = 3 * time.Second

func (r *RedisStore) Add(chatID, userID int64, userName, person, gender string) (Entry, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	base := fmt.Sprintf("men:%d", chatID)
	uid := strconv.FormatInt(userID, 10)
	if gender != "f" {
		gender = "m"
	}

	// Reuse an existing key that differs only in case («дима» → «Дима»).
	field := person
	names, err := r.c.HKeys(ctx, personKey(base, uid, gender)).Result()
	if err != nil {
		return Entry{}, 0, err
	}
	for _, n := range names {
		if strings.EqualFold(n, person) {
			field = n
			break
		}
	}

	var incr *redis.IntCmd
	_, err = r.c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.SAdd(ctx, base+":users", uid)
		p.HSet(ctx, base+":names", uid, userName)
		incr = p.HIncrBy(ctx, personKey(base, uid, gender), field, 1)
		return nil
	})
	if err != nil {
		return Entry{}, 0, err
	}
	e, err := r.entry(ctx, base, uid, userName)
	return e, int(incr.Val()), err
}

func (r *RedisStore) Top(chatID int64) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	base := fmt.Sprintf("men:%d", chatID)
	uids, err := r.c.SMembers(ctx, base+":users").Result()
	if err != nil {
		return nil, err
	}
	names, err := r.c.HGetAll(ctx, base+":names").Result()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, uid := range uids {
		e, err := r.entry(ctx, base, uid, names[uid])
		if err != nil {
			return nil, err
		}
		if e.Total() > 0 {
			out = append(out, e)
		}
	}
	sortEntries(out)
	return out, nil
}

func (r *RedisStore) entry(ctx context.Context, base, uid, name string) (Entry, error) {
	id, _ := strconv.ParseInt(uid, 10, 64)
	e := Entry{UserID: id, Name: name}
	var err error
	if e.Men, err = r.counts(ctx, personKey(base, uid, "m")); err != nil {
		return e, err
	}
	e.Women, err = r.counts(ctx, personKey(base, uid, "f"))
	return e, err
}

func (r *RedisStore) counts(ctx context.Context, key string) (map[string]int, error) {
	raw, err := r.c.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	m := make(map[string]int, len(raw))
	for k, v := range raw {
		n, _ := strconv.Atoi(v)
		m[k] = n
	}
	return m, nil
}

func personKey(base, uid, gender string) string { return base + ":" + uid + ":" + gender }
