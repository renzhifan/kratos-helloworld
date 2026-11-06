package server

import (
	"log/slog"

	"logisticsServer/internal/conf"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/middleware/validate"
	"github.com/go-kratos/kratos/v3/transport/http"

	"go.einride.tech/aip/fieldbehavior"
	"google.golang.org/protobuf/proto"
)

// NewHTTPServer new an HTTP server.
// proto 里的 google.api.http 注解已全部移除，当前没有注册任何 HTTP 路由，
// 所有对外能力只通过 gRPC 暴露。
func NewHTTPServer(c *conf.Server, a *conf.Auth, logger *slog.Logger) *http.Server {
	var opts = []http.ServerOption{
		http.Middleware(
			recovery.Recovery(),
			// tracing 必须在 logging 之前，请求日志才能读到 span 的 trace_id/span_id。
			tracing.Server(),
			logging.Server(logger),
			// 鉴权放在 logging 之后，鉴权失败也会记录访问日志。
			authMiddleware(a),
			validate.Validator(func(req any) error {
				if msg, ok := req.(proto.Message); ok {
					if err := fieldbehavior.ValidateRequiredFields(msg); err != nil {
						return err
					}
				}
				return nil
			}),
		),
	}
	if c.Http.Network != "" {
		opts = append(opts, http.Network(c.Http.Network))
	}
	if c.Http.Addr != "" {
		opts = append(opts, http.Address(c.Http.Addr))
	}
	if c.Http.Timeout != nil {
		opts = append(opts, http.Timeout(c.Http.Timeout.AsDuration()))
	}
	return http.NewServer(opts...)
}
