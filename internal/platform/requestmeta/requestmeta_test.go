package requestmeta

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRequestIDAndLogger(t *testing.T) {
	ctx := WithRequestID(context.Background(), "edge-123")
	if RequestID(ctx) != "edge-123" {
		t.Fatalf("request id = %q", RequestID(ctx))
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(previous)
	Logger(ctx).Warn("request failed")
	if !strings.Contains(output.String(), "request_id=edge-123") {
		t.Fatalf("log output = %q", output.String())
	}
}
