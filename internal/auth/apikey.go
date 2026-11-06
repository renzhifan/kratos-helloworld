// Package auth 提供基于 API Key 的服务端鉴权中间件，
// 调用方通过 HTTP header 或 gRPC metadata 传入密钥，与配置中的密钥常量时间比对。
package auth

import (
	"context"
	"crypto/subtle"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// APIKeyHeader 调用方传入密钥的 header/metadata 键，gRPC metadata 不区分大小写
const APIKeyHeader = "X-Api-Key"

const reasonUnauthorized = "UNAUTHORIZED"

var (
	// ErrMissingAPIKey 请求未携带密钥
	ErrMissingAPIKey = errors.Unauthorized(reasonUnauthorized, "缺少鉴权密钥 X-Api-Key")
	// ErrInvalidAPIKey 密钥不匹配，或服务端未配置密钥
	ErrInvalidAPIKey = errors.Unauthorized(reasonUnauthorized, "鉴权密钥无效")
)

// APIKey 服务端鉴权中间件，apiKey 为空时拒绝所有请求，避免漏配密钥导致接口裸奔
func APIKey(apiKey string) middleware.Middleware {
	expected := []byte(apiKey)
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (interface{}, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return nil, ErrMissingAPIKey
			}
			got := tr.RequestHeader().Get(APIKeyHeader)
			if got == "" {
				return nil, ErrMissingAPIKey
			}
			if len(expected) == 0 || subtle.ConstantTimeCompare([]byte(got), expected) != 1 {
				return nil, ErrInvalidAPIKey
			}
			return handler(ctx, req)
		}
	}
}
