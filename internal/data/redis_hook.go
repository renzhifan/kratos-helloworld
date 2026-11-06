package data

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisLogger records Redis commands with the caller's context, so the log
// carries the same trace_id/span_id as the request and SQL entries.
type redisLogger struct {
	logger *slog.Logger
}

// newRedisLogger wraps logger as a go-redis hook.
func newRedisLogger(logger *slog.Logger) redis.Hook {
	return &redisLogger{logger: logger}
}

// DialHook is left untouched; only executed commands are worth logging.
func (h *redisLogger) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

// ProcessHook logs a single command.
func (h *redisLogger) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		if skipLog([]redis.Cmder{cmd}, err) {
			return err
		}
		h.log(ctx, time.Since(start), cmd.Name(), cmd.String(), err)
		return err
	}
}

// ProcessPipelineHook logs a pipeline as one entry to keep log volume low.
func (h *redisLogger) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		if skipLog(cmds, err) {
			return err
		}
		names := make([]string, 0, len(cmds))
		for _, cmd := range cmds {
			names = append(names, cmd.Name())
		}
		h.log(ctx, time.Since(start), "pipeline", strings.Join(names, ","), err)
		return err
	}
}

// handshakeCommands 建连时客户端自动执行的命令，成功执行只是连接元信息，
// 与业务无关，不必进访问日志。
var handshakeCommands = map[string]struct{}{
	"hello":  {}, // 建连握手，包含 AUTH 认证
	"auth":   {}, // 老版本 Redis 单独认证
	"select": {}, // 切换数据库
	"client": {}, // CLIENT SETNAME / CLIENT SETINFO
}

// skipLog 判断这条记录是否没有保留价值：
//   - 建连握手命令执行成功；这些命令每次建连都会出现，只说明连接建立
//   - 老版本 Redis（<7.2）不支持 CLIENT SETINFO，go-redis 内部同样忽略该错误
//
// 握手失败（如密码错误）仍会记录，否则连接问题无从排查。
func skipLog(cmds []redis.Cmder, err error) bool {
	if len(cmds) == 0 {
		return false
	}
	if err != nil {
		if !strings.Contains(err.Error(), "setinfo") {
			return false
		}
		for _, cmd := range cmds {
			if cmd.Name() != "client" {
				return false
			}
		}
		return true
	}
	for _, cmd := range cmds {
		if _, ok := handshakeCommands[cmd.Name()]; !ok {
			return false
		}
	}
	return true
}

// maxArgsLogLength 单条命令参数的最大记录长度。SET 会把整个列表缓存
// 作为参数带进来，截断后日志才不会被大值撑爆。
const maxArgsLogLength = 512

// log writes the command: success is info level, failures are error level.
// redis.Nil is a cache miss, not a failure, so it stays at info.
func (h *redisLogger) log(ctx context.Context, elapsed time.Duration, command, detail string, err error) {
	attrs := []slog.Attr{
		slog.String("kind", "redis"),
		slog.String("command", command),
		slog.String("args", truncate(detail, maxArgsLogLength)),
		slog.Duration("elapsed", elapsed),
	}
	level := slog.LevelInfo
	if err != nil && !errors.Is(err, redis.Nil) {
		level = slog.LevelError
		attrs = append(attrs, slog.Any("error", err))
	}
	h.logger.LogAttrs(ctx, level, "redis command", attrs...)
}

// truncate cuts s to limit runes so multibyte characters stay intact.
func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
