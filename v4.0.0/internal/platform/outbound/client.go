// Package outbound 管理所有对外请求：带连接上限的 HTTP Client、共享限速器，
// 以及防 SSRF 的目标地址校验。对外抓取一律走这里，不要各自 new http.Client。
package outbound

import (
	"net/http"
	"time"
)

// NewClient 返回可共享且有连接上限的 HTTP Client。标准 Transport 默认没有
// MaxConnsPerHost 上限，突发请求可能在每个上游成倍创建套接字；调用方应在进程内复用此 Client。
func NewClient(timeout time.Duration, maxConnsPerHost int) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if maxConnsPerHost <= 0 {
		maxConnsPerHost = 12
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = max(64, maxConnsPerHost*4)
	transport.MaxIdleConnsPerHost = maxConnsPerHost
	transport.MaxConnsPerHost = maxConnsPerHost
	transport.IdleConnTimeout = 60 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = timeout
	transport.ExpectContinueTimeout = time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.ForceAttemptHTTP2 = true
	return &http.Client{Transport: transport, Timeout: timeout}
}
