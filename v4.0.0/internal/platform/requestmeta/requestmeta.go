package requestmeta

import (
	"context"
	"log/slog"
)

type requestIDKey struct{}

// WithRequestID 把入口生成的请求标识放入 context，让下游服务无需依赖 Gin 或 HTTP 包也能读取。
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

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
