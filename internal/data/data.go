package data

import (
	"context"
	"log/slog"

	"logisticsServer/internal/conf"
	"logisticsServer/internal/data/ent"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	_ "github.com/go-sql-driver/mysql"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewRedisClient, NewTodoRepo, NewTodoCache)

// Data holds the long-lived storage clients shared by repos.
type Data struct {
	db  *ent.Client
	rdb *redis.Client
}

// NewData opens the database client and returns it with a cleanup function.
func NewData(c *conf.Data, logger *slog.Logger, rdb *redis.Client) (*Data, func(), error) {
	dc := c.GetDatabase()
	drv, err := entsql.Open(dc.GetDriver(), dc.GetSource())
	if err != nil {
		return nil, nil, err
	}
	var driver dialect.Driver = drv
	// Debug mode routes every statement through the service logger so SQL
	// logs share the request's trace_id/span_id.
	if dc.GetDebug() {
		driver = newSQLDriver(driver, logger)
	}
	db := ent.NewClient(ent.Driver(driver))
	// Auto migration is a convenience for local development. In production,
	// apply schema changes as a separate reviewed step instead.
	if dc.GetAutoMigrate() {
		if err := db.Schema.Create(context.Background()); err != nil {
			db.Close()
			return nil, nil, err
		}
	}
	cleanup := func() {
		log.Info("closing the data resources")
		if err := db.Close(); err != nil {
			log.Error("failed closing the database", "err", err)
		}
	}
	return &Data{db: db, rdb: rdb}, cleanup, nil
}
