package tracing

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v3/transport"

	"go.opentelemetry.io/otel/trace"
)

// stubHeader 满足 transport.Header，用于模拟 gRPC metadata。
type stubHeader map[string]string

func (h stubHeader) Get(key string) string { return h[strings.ToLower(key)] }

func (h stubHeader) Set(key, value string) { h[strings.ToLower(key)] = value }

func (h stubHeader) Add(key, value string) { h.Set(key, value) }

func (h stubHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}

func (h stubHeader) Values(key string) []string {
	if v := h.Get(key); v != "" {
		return []string{v}
	}
	return nil
}

// stubTransport 只实现中间件用到的方法。
type stubTransport struct {
	header stubHeader
	reply  stubHeader
}

func (t *stubTransport) Kind() transport.Kind            { return transport.KindGRPC }
func (t *stubTransport) Endpoint() string                { return "grpc://127.0.0.1:50051" }
func (t *stubTransport) Operation() string               { return "/api.sf.v1.Sf/GetConfig" }
func (t *stubTransport) RequestHeader() transport.Header { return t.header }

func (t *stubTransport) ReplyHeader() transport.Header {
	if t.reply == nil {
		t.reply = stubHeader{}
	}
	return t.reply
}

func TestAdoptClientTraceID(t *testing.T) {
	tests := []struct {
		name   string
		header stubHeader
		want   string
	}{
		{"32 位十六进制", stubHeader{"x-trace-id": "0af7651916cd43dd8448eb211c80319c"}, "0af7651916cd43dd8448eb211c80319c"},
		{"大写与连字符", stubHeader{"x-trace-id": "0AF76519-16CD-43DD-8448-EB211C80319C"}, "0af7651916cd43dd8448eb211c80319c"},
		{"兼容 trace_id 键且补零", stubHeader{"trace_id": "8448eb211c80319c"}, "00000000000000008448eb211c80319c"},
		{"未携带", nil, ""},
		{"非法值", stubHeader{"x-trace-id": "abc"}, ""},
		{"全零", stubHeader{"x-trace-id": strings.Repeat("0", 32)}, ""},
		{"traceparent 优先", stubHeader{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"x-trace-id":  "0af7651916cd43dd8448eb211c80319c",
		}, ""},
	}
	mw := AdoptClientTraceID(slog.New(slog.DiscardHandler))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got trace.SpanContext
			handler := func(ctx context.Context, _ any) (any, error) {
				got = trace.SpanContextFromContext(ctx)
				return nil, nil
			}
			ctx := transport.NewServerContext(context.Background(), &stubTransport{header: tt.header})
			if _, err := mw(handler)(ctx, nil); err != nil {
				t.Fatalf("middleware error: %v", err)
			}
			if tt.want == "" {
				if got.TraceID().IsValid() {
					t.Fatalf("不应注入 trace_id，实际得到 %s", got.TraceID())
				}
				return
			}
			if !got.IsRemote() {
				t.Fatalf("期望注入远端 span 上下文")
			}
			if !got.SpanID().IsValid() {
				t.Fatalf("占位 span_id 必须非零")
			}
			if got.TraceID().String() != tt.want {
				t.Fatalf("trace_id = %s, want %s", got.TraceID(), tt.want)
			}
		})
	}
}

func TestEchoTraceID(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	if err != nil {
		t.Fatalf("parse trace id: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  newRemoteSpanID(),
	})
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"有 span 时回写", trace.ContextWithSpanContext(context.Background(), sc), "0af7651916cd43dd8448eb211c80319c"},
		{"无 span 时不回写", context.Background(), ""},
	}
	mw := EchoTraceID()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &stubTransport{}
			ctx := transport.NewServerContext(tt.ctx, tr)
			handler := func(context.Context, any) (any, error) { return nil, nil }
			if _, err := mw(handler)(ctx, nil); err != nil {
				t.Fatalf("middleware error: %v", err)
			}
			if got := tr.reply.Get(TraceIDHeader); got != tt.want {
				t.Fatalf("响应头 %s = %q, want %q", TraceIDHeader, got, tt.want)
			}
		})
	}
}
