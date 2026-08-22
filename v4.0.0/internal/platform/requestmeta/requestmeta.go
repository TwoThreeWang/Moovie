// Package requestmeta 在 context 里透传请求标识，让业务层打日志时不必依赖 Gin。
package requestmeta

import (
	"context"
	"log/slog"
)

// requestIDKey 是请求 ID 在 context 里的键类型。
type requestIDKey struct{}

// WithRequestID 把入口生成的请求标识放入 context，让下游服务无需依赖 Gin 或 HTTP 包也能读取。
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestID 取出请求标识，没有则返回空串。
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}

// Logger 返回关联当前请求的日志器；没有请求标识时仍可供后台任务正常使用。
func Logger(ctx context.Context) *slog.Logger {
	if requestID := RequestID(ctx); requestID != "" {
		return slog.Default().With("request_id", requestID)
	}
	return slog.Default()
}
