package data

import (
	"context"
	"log/slog"
	"time"

	"entgo.io/ent/dialect"
)

// sqlDriver 包装 dialect.Driver，开发模式下把 SQL、参数和耗时写入日志。
// 日志使用请求的 context，因此能带上当前链路的 trace_id/span_id。
type sqlDriver struct {
	dialect.Driver
	logger *slog.Logger
}

// newSQLDriver wraps d so every statement it executes is logged through logger.
func newSQLDriver(d dialect.Driver, logger *slog.Logger) dialect.Driver {
	return &sqlDriver{Driver: d, logger: logger}
}

// Exec implements dialect.Driver.
func (d *sqlDriver) Exec(ctx context.Context, query string, args, v any) error {
	return d.log(ctx, "exec", query, args, func() error { return d.Driver.Exec(ctx, query, args, v) })
}

// Query implements dialect.Driver.
func (d *sqlDriver) Query(ctx context.Context, query string, args, v any) error {
	return d.log(ctx, "query", query, args, func() error { return d.Driver.Query(ctx, query, args, v) })
}

// Tx implements dialect.Driver.
func (d *sqlDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &sqlTx{Tx: tx, driver: d}, nil
}

// sqlTx 让事务内的语句同样记录 SQL 日志。
type sqlTx struct {
	dialect.Tx
	driver *sqlDriver
}

// Exec implements dialect.Tx.
func (t *sqlTx) Exec(ctx context.Context, query string, args, v any) error {
	return t.driver.log(ctx, "tx.exec", query, args, func() error { return t.Tx.Exec(ctx, query, args, v) })
}

// Query implements dialect.Tx.
func (t *sqlTx) Query(ctx context.Context, query string, args, v any) error {
	return t.driver.log(ctx, "tx.query", query, args, func() error { return t.Tx.Query(ctx, query, args, v) })
}

// log 执行语句并记录 SQL 日志：成功为 info 级别，失败为 error 级别，与请求
// 日志一起落在 info 文件中。
func (d *sqlDriver) log(ctx context.Context, op, query string, args any, fn func() error) error {
	start := time.Now()
	err := fn()
	attrs := []slog.Attr{
		slog.String("kind", "database"),
		slog.String("operation", op),
		slog.String("sql", query),
		slog.Any("args", args),
		slog.Duration("elapsed", time.Since(start)),
	}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.Any("error", err))
	}
	d.logger.LogAttrs(ctx, level, "database query", attrs...)
	return err
}
