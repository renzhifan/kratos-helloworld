package data

import (
	entsql "entgo.io/ent/dialect/sql"
	"github.com/XSAM/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// openDB 打开数据库连接，并在 database/sql 驱动层挂上 OpenTelemetry 包装：
// 每条 SQL 都会作为 client span 挂在当前请求的链路上，随服务端 span 一起
// 上报。driver 取自 data.database.driver，与 configs/config.yaml 保持一致。
func openDB(driver, source string) (*entsql.Driver, error) {
	db, err := otelsql.Open(driver, source,
		// 新版语义约定用 db.system.name 标记数据库类型，值为驱动名（mysql）。
		otelsql.WithAttributes(semconv.DBSystemNameKey.String(driver)),
		// 一次查询在驱动层会拆出若干 span，这里只做降噪和归层级：rows 挂到
		// 产生它的 query 下，一次性的建连/重置会话不再单独上报。
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			RowsChildOfQuery:     true,
			OmitConnectorConnect: true,
			OmitConnResetSession: true,
			DisableErrSkip:       true,
		}),
	)
	if err != nil {
		return nil, err
	}
	return entsql.OpenDB(driver, db), nil
}
