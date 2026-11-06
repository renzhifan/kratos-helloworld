package server

import (
	"log/slog"

	encryptv1 "logisticsServer/api/encrypt/v1"
	v1 "logisticsServer/api/todo/v1"
	"logisticsServer/internal/conf"
	"logisticsServer/internal/service"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/middleware/validate"
	"github.com/go-kratos/kratos/v3/transport/grpc"

	"go.einride.tech/aip/fieldbehavior"
	"google.golang.org/protobuf/proto"
)

// NewGRPCServer new a gRPC server.
func NewGRPCServer(c *conf.Server, logger *slog.Logger, todo *service.TodoService, encrypt *service.EncryptService) *grpc.Server {
	var opts = []grpc.ServerOption{
		grpc.Middleware(
			recovery.Recovery(),
			// tracing 必须在 logging 之前，请求日志才能读到 span 的 trace_id/span_id。
			tracing.Server(),
			logging.Server(logger),
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
	if c.Grpc.Network != "" {
		opts = append(opts, grpc.Network(c.Grpc.Network))
	}
	if c.Grpc.Addr != "" {
		opts = append(opts, grpc.Address(c.Grpc.Addr))
	}
	if c.Grpc.Timeout != nil {
		opts = append(opts, grpc.Timeout(c.Grpc.Timeout.AsDuration()))
	}
	srv := grpc.NewServer(opts...)
	v1.RegisterTodoServiceServer(srv, todo)
	encryptv1.RegisterEncryptServer(srv, encrypt)
	return srv
}
