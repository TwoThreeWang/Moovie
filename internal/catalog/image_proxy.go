package catalog

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// imageProxyPrefix 是代理链接里的固定前缀，用来挡掉随手拼 base64 的请求。
const imageProxyPrefix = "r76RqSIVvUryzx"

// 图片代理的安全名单：禁止访问内网和保留地址段，防止被当作内网探测跳板。
var (
	errUnsafeImageProxyTarget = errors.New("unsafe image proxy target")
	blockedImageProxyRanges   = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("ff00::/8"),
	}
)

// hotlinkDeniedSVG 是被判定为盗链时返回的占位图。
const hotlinkDeniedSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="600" viewBox="0 0 400 600"><rect width="400" height="600" fill="#111827"/><text x="200" y="300" fill="#f9fafb" font-family="system-ui,sans-serif" font-size="26" text-anchor="middle" dominant-baseline="middle">仅限 Moovie 内部使用</text></svg>`

// proxyImage 代理外站图片。豆瓣图片有防盗链，必须由服务端带 Referer 去取；
// 同时这个接口本身也要防着被别人当免费图床用（同源校验 + 目标地址白名单校验）。
func (handler *Handler) proxyImage(c *gin.Context) {
	c.Header("Vary", "Sec-Fetch-Site, Sec-Fetch-Dest, Sec-Fetch-Mode, Referer")
	c.Header("Cache-Control", "private, no-store")
	if !isSameOriginImageRequest(c.Request, handler.config.SiteURL) {
		writeHotlinkDeniedSVG(c)
		return
	}
	targetURL, err := decodeProxyImageURL(c.Param("url"))
	if err != nil {
		catalogAPIError(c, http.StatusBadRequest, "非法的图片代理链接")
		return
	}
	targetURL, err = validateImageProxyTarget(targetURL)
	if err != nil {
		c.Status(http.StatusForbidden)
		return
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		catalogAPIError(c, http.StatusInternalServerError, "创建请求失败")
		return
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	request.Header.Set("Referer", imageProxyReferer(targetURL))
	client := *handler.httpClient
	previousCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(redirect *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("stopped after 3 redirects")
		}
		normalized, err := validateImageProxyTarget(redirect.URL.String())
		if err != nil {
			return err
		}
		redirect.URL, _ = url.Parse(normalized)
		redirect.Header.Set("Referer", imageProxyReferer(normalized))
		if previousCheckRedirect != nil {
			return previousCheckRedirect(redirect, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errUnsafeImageProxyTarget) {
			c.Status(http.StatusForbidden)
			return
		}
		catalogAPIError(c, http.StatusInternalServerError, "请求图片失败")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		c.Status(response.StatusCode)
		return
	}
	reader := bufio.NewReader(response.Body)
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		sample, _ := reader.Peek(512)
		contentType = http.DetectContentType(sample)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		c.Status(http.StatusUnsupportedMediaType)
		return
	}
	c.Header("Cache-Control", "private, max-age=2592000")
	c.Header("Expires", time.Now().AddDate(0, 0, 30).Format(http.TimeFormat))
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, response.ContentLength, contentType, reader, nil)
}

// isSameOriginImageRequest 用浏览器发的 Sec-Fetch-* 头判断是不是本站页面在加载图片。
func isSameOriginImageRequest(request *http.Request, siteURL string) bool {
	site := request.Header.Get("Sec-Fetch-Site")
	destination := request.Header.Get("Sec-Fetch-Dest")
	mode := request.Header.Get("Sec-Fetch-Mode")
	if site != "" {
		return site == "same-origin" && destination == "image" && mode == "no-cors"
	}
	if destination != "" && destination != "image" || mode != "" && mode != "no-cors" {
		return false
	}
	referer, refererErr := url.Parse(request.Referer())
	configured, configuredErr := url.Parse(siteURL)
	if refererErr != nil || configuredErr != nil || referer.Hostname() == "" || configured.Hostname() == "" {
		return false
	}
	return strings.EqualFold(referer.Scheme, configured.Scheme) &&
		strings.EqualFold(referer.Hostname(), configured.Hostname()) &&
		effectiveURLPort(referer) == effectiveURLPort(configured)
}

func effectiveURLPort(parsed *url.URL) string {
	if parsed.Port() != "" {
		return parsed.Port()
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return "443"
	}
	return "80"
}

// writeHotlinkDeniedSVG 返回占位图，而不是报错，免得别人站上出现一堆裂图报警。
func writeHotlinkDeniedSVG(c *gin.Context) {
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(hotlinkDeniedSVG))
}

// decodeProxyImageURL 去掉前缀并 base64 解码出真实图片地址。
func decodeProxyImageURL(value string) (string, error) {
	if !strings.HasPrefix(value, imageProxyPrefix) {
		return "", fmt.Errorf("invalid proxy prefix")
	}
	encoded := strings.TrimPrefix(value, imageProxyPrefix)
	decoders := []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding}
	for _, decoder := range decoders {
		if decoded, err := decoder.DecodeString(encoded); err == nil {
			return string(decoded), nil
		}
	}
	return "", fmt.Errorf("invalid proxy encoding")
}

// validateImageProxyTarget 只允许 http/https 且域名可解析到公网地址，防止被用来探测内网（SSRF）。
func validateImageProxyTarget(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errUnsafeImageProxyTarget
	}
	if port := parsed.Port(); port != "" && !((parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443")) {
		return "", errUnsafeImageProxyTarget
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || host == "metadata.google.internal" ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") ||
		strings.HasSuffix(host, ".home") || strings.HasSuffix(host, ".home.arpa") {
		return "", errUnsafeImageProxyTarget
	}
	if address, parseErr := netip.ParseAddr(host); parseErr == nil {
		if !isPublicImageProxyIP(address) {
			return "", errUnsafeImageProxyTarget
		}
	} else if !strings.Contains(host, ".") {
		return "", errUnsafeImageProxyTarget
	}
	if host == "img9.doubanio.com" {
		parsed.Host = "img3.doubanio.com"
	}
	return parsed.String(), nil
}

// imageProxyReferer 按目标站点补上对应的 Referer，绕过对方的防盗链。
func imageProxyReferer(rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	if strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".doubanio.com") {
		return "https://movie.douban.com/"
	}
	return parsed.Scheme + "://" + parsed.Host + "/"
}

// newImageProxyHTTPClient 给 Client 换上受限的 Transport：拨号阶段再校验一次 IP，
// 防止 DNS 解析结果在校验之后被改成内网地址。
func newImageProxyHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	hardened := *client
	var transport *http.Transport
	switch current := client.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = current.Clone()
	default:
		return &hardened
	}
	direct := transport.Clone()
	direct.Proxy = nil
	direct.DialContext = safeImageProxyDialContext
	// Clash/WARP Fake-IP 环境必须保留既有代理解析；无代理请求才由本进程解析并固定公网 IP。
	hardened.Transport = &imageProxyTransport{direct: direct, proxied: transport, proxy: transport.Proxy}
	return &hardened
}

// imageProxyTransport 只在图片代理请求上启用安全拨号，其他请求原样透传。
type imageProxyTransport struct {
	direct  http.RoundTripper
	proxied http.RoundTripper
	proxy   func(*http.Request) (*url.URL, error)
}

// RoundTrip 在真正发请求前校验目标地址不是内网地址。
func (transport *imageProxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.proxy != nil {
		proxyURL, err := transport.proxy(request)
		if err != nil {
			return nil, err
		}
		if proxyURL != nil {
			return transport.proxied.RoundTrip(request)
		}
	}
	return transport.direct.RoundTrip(request)
}

// CloseIdleConnections 关闭空闲连接。
func (transport *imageProxyTransport) CloseIdleConnections() {
	if closer, ok := transport.direct.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	if closer, ok := transport.proxied.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// safeImageProxyDialContext 逐个尝试解析出的 IP，跳过所有内网/保留地址。
func safeImageProxyDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid address", errUnsafeImageProxyTarget)
	}
	var addresses []netip.Addr
	if parsed, parseErr := netip.ParseAddr(host); parseErr == nil {
		addresses = []netip.Addr{parsed}
	} else {
		addresses, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w: no address", errUnsafeImageProxyTarget)
	}
	for _, candidate := range addresses {
		if !isPublicImageProxyIP(candidate) {
			return nil, fmt.Errorf("%w: non-public address", errUnsafeImageProxyTarget)
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, candidate := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	return nil, lastErr
}

// isPublicImageProxyIP 判断是否是可以访问的公网地址。
func isPublicImageProxyIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.Zone() != "" || !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, blocked := range blockedImageProxyRanges {
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}

// catalogAPIError 统一的 JSON 错误响应。
func catalogAPIError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"code": status, "message": message, "data": nil, "success": false})
}
