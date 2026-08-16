package outbound

import (
	"net/http"
	"testing"
	"time"
)

func TestNewClientBoundsConnectionsAndTimeouts(t *testing.T) {
	client := NewClient(7*time.Second, 9)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if client.Timeout != 7*time.Second || transport.MaxConnsPerHost != 9 || transport.MaxIdleConnsPerHost != 9 ||
		transport.MaxIdleConns < 64 || transport.ResponseHeaderTimeout != 7*time.Second || transport.MaxResponseHeaderBytes != 64<<10 {
		t.Fatalf("client/transport = timeout:%s transport:%+v", client.Timeout, transport)
	}
}
