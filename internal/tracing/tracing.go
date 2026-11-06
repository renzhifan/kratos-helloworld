// Package tracing 初始化全局 OpenTelemetry TracerProvider。即使关闭上报，
// 本地也会生成 trace_id/span_id，日志里的链路字段才不会为空。
package tracing

import (
	"context"
	"errors"
	"time"

	"logisticsServer/internal/conf"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// shutdownTimeout 退出时等待剩余 span 上报完成的最长时间。
const shutdownTimeout = 5 * time.Second

// NewProvider 创建并设置全局 TracerProvider。enabled 为 true 时通过 OTLP/HTTP
// 把链路数据上报到 endpoint；为 false 时不上报，但采样器仍会为每个请求生成
// trace_id/span_id。返回的 shutdown 用于退出前刷新剩余 span。
func NewProvider(c *conf.Trace, serviceName string) (func(), error) {
	// W3C traceparent/baggage 是跨服务传递 trace_id 的标准载体，显式设置
	// 以免依赖全局默认值。
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	opts := []sdktrace.TracerProviderOption{
		// 全量采样，保证关闭上报时请求日志依然带 trace_id/span_id。
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(resource.NewSchemaless(
			semconv.ServiceNameKey.String(serviceName),
		)),
	}
	if c.GetEnabled() {
		if c.GetEndpoint() == "" {
			return nil, errors.New("trace.endpoint is required when trace.enabled is true")
		}
		exp, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(c.GetEndpoint()))
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = tp.Shutdown(ctx)
	}, nil
}
