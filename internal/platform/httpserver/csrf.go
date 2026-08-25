package httpserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
)

// CSRF token 的 Cookie 名和请求头名。
const (
	csrfCookieName = "csrf_token"
	csrfHeaderName = "X-CSRF-Token"
)

// csrfTokenPattern 限定 token 是 64 位十六进制。
var csrfTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// csrfProtection 采用 double-submit cookie 方案：Cookie 里放随机 token，
// 写操作必须在 X-CSRF-Token 头或表单字段里回传同一个值。
// GET/HEAD 等安全方法直接放行，只负责补发 Cookie。
func csrfProtection(secureCookie bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(csrfCookieName)
		if err != nil || !csrfTokenPattern.MatchString(token) {
			token, err = newCSRFToken()
			if err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "安全令牌生成失败"})
				return
			}
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     csrfCookieName,
				Value:    token,
				Path:     "/",
				MaxAge:   24 * 60 * 60,
				Secure:   secureCookie,
				HttpOnly: false, // Double-submit token must be readable by the same-origin client runtime.
				SameSite: http.SameSiteLaxMode,
			})
		}

		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		submitted := c.GetHeader(csrfHeaderName)
		if submitted == "" {
			submitted = c.PostForm(csrfCookieName)
		}
		if len(submitted) != len(token) || subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "安全令牌无效，请刷新页面后重试"})
			return
		}
		c.Next()
	}
}

// newCSRFToken 生成 32 字节随机 token 的十六进制表示。
func newCSRFToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// isSafeMethod 判断是否为不改变状态的 HTTP 方法。
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
