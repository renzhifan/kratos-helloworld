package data

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"logisticsServer/internal/conf"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// defaultPingTimeout 连接检查的最小超时时间。dial_timeout 通常按单次
// 建连配置，公网抖动时容易过紧，因此 Ping 至少给它一个下限。
const defaultPingTimeout = 3 * time.Second

// NewRedisClient 按 data.redis 配置创建 Redis 客户端，并做一次连通性检查，
// 避免把连接问题推迟到第一次读写。连接参数来自 configs/config.yaml：
// addr/password/db/dial_timeout/read_timeout/write_timeout。
func NewRedisClient(c *conf.Data, logger *slog.Logger) (*redis.Client, func(), error) {
	rc := c.GetRedis()
	opts := &redis.Options{
		Addr:     rc.GetAddr(),
		Password: rc.GetPassword(),
		DB:       int(rc.GetDb()),
		// 建连时客户端会上报库名版本（CLIENT SETINFO），该命令 Redis 7.2 才支持。
		// 线上实例是 6.0，会返回 "unknown subcommand 'setinfo'"，go-redis 内部
		// 会忽略这个错误，但命令仍会经过日志 hook，表现为每次建连一条 ERROR。
		DisableIdentity: true,
	}
	if d := rc.GetDialTimeout().AsDuration(); d > 0 {
		opts.DialTimeout = d
	}
	if d := rc.GetReadTimeout().AsDuration(); d > 0 {
		opts.ReadTimeout = d
	}
	if d := rc.GetWriteTimeout().AsDuration(); d > 0 {
		opts.WriteTimeout = d
	}
	rdb := redis.NewClient(opts)

	timeout := opts.DialTimeout
	if timeout < defaultPingTimeout {
		timeout = defaultPingTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, nil, fmt.Errorf("ping redis: %w", err)
	}
	logger.Info("redis connected", "addr", opts.Addr, "db", opts.DB)
	// 链路追踪 hook 先注册，后面的日志 hook 才能读到 redis span 的 span_id：
	// 每条命令都会生成一个 client span，挂在当前请求 span 下上报。
	if err := redisotel.InstrumentTracing(rdb); err != nil {
		_ = rdb.Close()
		return nil, nil, fmt.Errorf("instrument redis tracing: %w", err)
	}
	// Register after the startup ping so the health check does not add a
	// context-free entry to the logs.
	rdb.AddHook(newRedisLogger(logger))

	cleanup := func() {
		if err := rdb.Close(); err != nil {
			logger.Error("failed closing the redis client", "err", err)
		}
	}
	return rdb, cleanup, nil
}
