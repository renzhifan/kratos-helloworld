// Package logger 按配置构建全局 slog logger：控制输出级别，按级别拆分写入
// 日志目录（如 20261106-info.log、20261106-error.log），并把当前请求的
// trace_id/span_id 附加到每条日志上。
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"logisticsServer/internal/conf"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/log"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
)

const (
	// defaultMaxAge 未配置 max_age 时日志文件的保留天数。
	defaultMaxAge = 7
	// rotationTime 日志文件切分周期。
	rotationTime = 24 * time.Hour
	// fatalLevel kratos 的 fatal 级别在 error 之上 4 级。
	fatalLevel = slog.LevelError + 4
	// maxLevel 级别区间上界，覆盖所有更高级别的日志。
	maxLevel = slog.Level(127)
	// timeLayout 日志时间格式，如 2026-11-03 17:00:52。
	timeLayout = "2006-01-02 15:04:05"
)

// cstZone 中国标准时间（东八区），日志时间统一按该时区输出。
var cstZone = time.FixedZone("CST", 8*60*60)

// cstClock 让日志文件按中国时间切分，容器时区为 UTC 时文件名日期也与日志
// 时间一致。
type cstClock struct{}

// Now implements rotatelogs.Clock.
func (cstClock) Now() time.Time { return time.Now().In(cstZone) }

// logSink 描述一类日志级别的落盘文件与终端输出目标。
type logSink struct {
	level   slog.Level // 该文件记录的最低级别
	pattern string     // 文件名模板，如 %Y%m%d-info.log
	term    io.Writer  // 同时输出的终端
}

// sinks 按级别从低到高排列，每个文件只记录 [level, 下一个级别) 的日志。
// 数据表 SQL 日志与请求日志都是 info 级别，一起写入 info 文件。
var sinks = []logSink{
	{level: slog.LevelInfo, pattern: "%Y%m%d-info.log", term: os.Stdout},
	{level: slog.LevelWarn, pattern: "%Y%m%d-warn.log", term: os.Stdout},
	{level: slog.LevelError, pattern: "%Y%m%d-error.log", term: os.Stderr},
	{level: fatalLevel, pattern: "%Y%m%d-fatal.log", term: os.Stderr},
}

// New 构建服务使用的 slog logger。level 决定最低输出级别；path 非空时日志
// 按天、按级别拆分成多个文件并同时输出到标准输出（error 及以上输出到标准
// 错误）。返回的 cleanup 用于关闭日志文件。
func New(c *conf.Logger, serviceID, serviceName, serviceVersion string) (*slog.Logger, func(), error) {
	handler, cleanup, err := newHandler(c)
	if err != nil {
		return nil, nil, err
	}
	logger := log.NewLogger(handler, log.WithExtractor(tracing.TraceAttrs)).With(
		slog.String("service.id", serviceID),
		slog.String("service.name", serviceName),
		slog.String("service.version", serviceVersion),
	)
	return logger, cleanup, nil
}

// newHandler 按级别拆分日志：低于 level 的日志直接丢弃；path 非空时每个级别
// 写入独立文件，并按天切分。
func newHandler(c *conf.Logger) (slog.Handler, func(), error) {
	level := log.ParseLevel(c.GetLevel())
	path := c.GetPath()
	if path == "" {
		// 未配置日志目录时只输出到标准输出。
		handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource:   true,
			Level:       level,
			ReplaceAttr: replaceAttr,
		})
		return handler, func() {}, nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, nil, err
	}
	maxAge := c.GetMaxAge()
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	retention := time.Duration(maxAge) * 24 * time.Hour

	var (
		handlers []slog.Handler
		closers  []io.Closer
	)
	cleanup := func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}
	for i, sink := range sinks {
		// 配置级别更高时提升该文件的区间下限；区间为空说明该级别无需落盘。
		lower := sink.level
		if lower < level {
			lower = level
		}
		upper := maxLevel
		if i+1 < len(sinks) {
			upper = sinks[i+1].level
		}
		if lower >= upper {
			continue
		}
		rotating, err := rotatelogs.New(
			filepath.Join(path, sink.pattern),
			rotatelogs.WithMaxAge(retention),
			rotatelogs.WithRotationTime(rotationTime),
			rotatelogs.WithClock(cstClock{}),
		)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		closers = append(closers, rotating)
		handlers = append(handlers, &rangeHandler{
			Handler: slog.NewTextHandler(io.MultiWriter(rotating, sink.term), &slog.HandlerOptions{
				AddSource:   true,
				Level:       lower,
				ReplaceAttr: replaceAttr,
			}),
			min: lower,
			max: upper,
		})
	}
	return multiHandler(handlers), cleanup, nil
}

// replaceAttr 把日志时间统一格式化为中国时间，如 2026-11-03 17:00:52。
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.String(slog.TimeKey, a.Value.Time().In(cstZone).Format(timeLayout))
	}
	return a
}

// rangeHandler 只处理 [min, max) 区间的日志，把不同级别的记录分流到不同文件。
type rangeHandler struct {
	slog.Handler
	min slog.Level
	max slog.Level
}

// Enabled implements slog.Handler.
func (h *rangeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.min && level < h.max && h.Handler.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *rangeHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.Handler.Handle(ctx, record)
}

// WithAttrs implements slog.Handler.
func (h *rangeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &rangeHandler{Handler: h.Handler.WithAttrs(attrs), min: h.min, max: h.max}
}

// WithGroup implements slog.Handler.
func (h *rangeHandler) WithGroup(name string) slog.Handler {
	return &rangeHandler{Handler: h.Handler.WithGroup(name), min: h.min, max: h.max}
}

// multiHandler 把一条记录分发给所有级别匹配的 handler。
type multiHandler []slog.Handler

// Enabled implements slog.Handler.
func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle implements slog.Handler.
func (m multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, h := range m {
		if !h.Enabled(ctx, record.Level) {
			continue
		}
		if err := h.Handle(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs implements slog.Handler.
func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make(multiHandler, len(m))
	for i, h := range m {
		handlers[i] = h.WithAttrs(attrs)
	}
	return handlers
}

// WithGroup implements slog.Handler.
func (m multiHandler) WithGroup(name string) slog.Handler {
	handlers := make(multiHandler, len(m))
	for i, h := range m {
		handlers[i] = h.WithGroup(name)
	}
	return handlers
}
