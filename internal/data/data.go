package data

import (
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewRedisClient, NewSfRepo)

// Data holds the long-lived storage clients shared by repos.
type Data struct {
	rdb *redis.Client
}

// NewData bundles the storage clients that repos depend on.
func NewData(rdb *redis.Client) *Data {
	return &Data{rdb: rdb}
}
