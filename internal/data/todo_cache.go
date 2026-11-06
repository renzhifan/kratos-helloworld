package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"logisticsServer/internal/biz"

	"github.com/redis/go-redis/v9"
)

// todoCache stores todo list pages in Redis as JSON. It implements
// biz.TodoCache; the DO is serialized directly because its fields
// (uuid.UUID, time.Time) all round-trip through encoding/json.
type todoCache struct {
	rdb *redis.Client
}

// NewTodoCache creates a Redis-backed biz.TodoCache.
func NewTodoCache(rdb *redis.Client) biz.TodoCache {
	return &todoCache{rdb: rdb}
}

// Get returns the cached page for key, reporting a miss as ok=false.
func (c *todoCache) Get(ctx context.Context, key string) ([]*biz.Todo, bool, error) {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var todos []*biz.Todo
	if err := json.Unmarshal(raw, &todos); err != nil {
		return nil, false, err
	}
	return todos, true, nil
}

// Set writes the page under key with the given TTL.
func (c *todoCache) Set(ctx context.Context, key string, todos []*biz.Todo, ttl time.Duration) error {
	raw, err := json.Marshal(todos)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, raw, ttl).Err()
}
