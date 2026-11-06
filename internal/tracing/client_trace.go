package tracing

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"

	"go.opentelemetry.io/otel/trace"
)

// TraceIDHeader 客户端传入自己链路 ID 的 header / gRPC metadata 键。
// HTTP 头不区分大小写，gRPC metadata 一侧统一小写。
const TraceIDHeader = "x-trace-id"

// traceparentHeader W3C 标准链路头，客户端带它时优先级高于 TraceIDHeader。
const traceparentHeader = "traceparent"

// clientTraceIDHeaders 兼容几种常见的自定义写法，按顺序取第一个非空值。
var clientTraceIDHeaders = []string{TraceIDHeader, "trace_id", "trace-id"}

// AdoptClientTraceID 返回一个中间件：请求里带了客户端 trace_id 时，
// 把它当作父链路的 trace_id，服务端生成的所有 span（含日志里的 trace_id）
// 都会落在客户端这条链路上，从而可以用客户端请求的 trace_id 反查服务端。
//
// 必须放在 oteltracing.Server() 之前：tracing 中间件才会把注入的远端
// span 作为父节点。客户端带了标准 traceparent 时以 traceparent 为准。
func AdoptClientTraceID(logger *slog.Logger) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}
			header := tr.RequestHeader()
			// 标准载体已给出父链路，交给 oteltracing 的 TraceContext 传播器处理。
			if header.Get(traceparentHeader) != "" {
				return handler(ctx, req)
			}
			raw := clientTraceID(header)
			if raw == "" {
				return handler(ctx, req)
			}
			traceID, err := parseTraceID(raw)
			if err != nil {
				// 非法值不阻断请求，退回服务端自己生成 trace_id。
				logger.WarnContext(ctx, "ignoring invalid client trace_id",
					"header", TraceIDHeader, "value", raw, "err", err)
				return handler(ctx, req)
			}
			ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				// 服务端看不到客户端的 span_id，造一个非零占位值充当父 span。
				SpanID:     newRemoteSpanID(),
				TraceFlags: trace.FlagsSampled,
				Remote:     true,
			}))
			return handler(ctx, req)
		}
	}
}

// EchoTraceID 返回一个中间件：把本次请求实际使用的 trace_id 回写到响应
// （HTTP response header / gRPC header metadata）。客户端没带 trace_id 时，
// 也能从响应里拿到服务端生成的链路 ID，用于后续排查。
//
// 必须放在 oteltracing.Server() 之后，此时 ctx 里才有本次请求的 span。
// HTTP 响应头需要在 handler 写 body 之前设置，因此统一在入口处写。
func EchoTraceID() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				if sc := trace.SpanContextFromContext(ctx); sc.TraceID().IsValid() {
					tr.ReplyHeader().Set(TraceIDHeader, sc.TraceID().String())
				}
			}
			return handler(ctx, req)
		}
	}
}

// clientTraceID 按优先级取出客户端传入的 trace_id 原文。
func clientTraceID(header transport.Header) string {
	for _, key := range clientTraceIDHeaders {
		if v := strings.TrimSpace(header.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

// parseTraceID 把客户端传入的字符串规整成 128 位 trace_id：
// 支持带连字符的 UUID 写法，16 位十六进制左侧补零，统一转小写。
func parseTraceID(raw string) (trace.TraceID, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "-", "")
	if len(s) == 16 {
		s = strings.Repeat("0", 16) + s
	}
	if len(s) != 32 {
		return trace.TraceID{}, errors.New("trace_id must be 16 or 32 hex characters")
	}
	return trace.TraceIDFromHex(s)
}

// newRemoteSpanID 生成非零的 span_id，作为客户端链路的占位父 span。
func newRemoteSpanID() trace.SpanID {
	var sid trace.SpanID
	for !sid.IsValid() {
		_, _ = rand.Read(sid[:])
	}
	return sid
}
