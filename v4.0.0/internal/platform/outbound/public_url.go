package outbound

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ValidatePublicHTTPURL 拒绝可直接访问本机、私网或链路本地服务的目标地址。
// 调用方还应使用 PublicRedirectClient，防止原本允许的来源通过重定向越过这一边界。
func ValidatePublicHTTPURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid public HTTP URL")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("non-public HTTP host")
	}
	if address := net.ParseIP(host); address != nil {
		if !isPublicIP(address) {
			return fmt.Errorf("non-public HTTP address")
		}
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") {
		return fmt.Errorf("non-public HTTP host")
	}
	return nil
}

func PublicRedirectClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := ValidatePublicHTTPURL(request.URL.String()); err != nil {
			return err
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

func isPublicIP(address net.IP) bool {
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsUnspecified() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return false
	}
	for _, rawRange := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "2001:db8::/32"} {
		_, blocked, _ := net.ParseCIDR(rawRange)
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}
