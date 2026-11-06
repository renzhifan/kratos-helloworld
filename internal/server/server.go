package server

import (
	"logisticsServer/internal/auth"
	"logisticsServer/internal/conf"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/google/wire"
)

// ProviderSet is server providers.
var ProviderSet = wire.NewSet(NewGRPCServer, NewHTTPServer)

// authMiddleware 所有 HTTP/gRPC 接口统一校验 API Key。
// 如需放行探活等接口，用 selector.Server(auth.APIKey(...)).Match(...).Build()
// 按 Operation 列入白名单。
func authMiddleware(c *conf.Auth) middleware.Middleware {
	return auth.APIKey(c.GetApiKey())
}
