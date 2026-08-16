package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	contextUserID            = "user_id"
	contextIdentityValidated = "identity_validated"
	contextClaims            = "auth_claims"
	contextSessionRenewed    = "auth_session_renewed"
)

type Claims struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Expiry int64  `json:"exp"`
	Issued int64  `json:"iat"`
}

func Optional(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 数据库身份中间件可能已经验证或作废此 token。后续模块即使再次安装
		// Optional，也不能恢复已经失效的 claims。
		if _, checked := c.Get(contextIdentityValidated); checked {
			c.Next()
			return
		}
		if claims, err := Extract(c.Request, secret, time.Now()); err == nil {
			setClaims(c, claims)
		}
		c.Next()
	}
}

func Require(secret string, secureCookie bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := Extract(c.Request, secret, time.Now())
		if err != nil {
			rejectUnauthenticated(c)
			return
		}
		if validated, checked := identityValidation(c); checked {
			if !validated || UserID(c) != claims.UserID {
				rejectUnauthenticated(c)
				return
			}
			claims.Email = contextString(c, "email")
			claims.Role = contextString(c, "role")
		} else {
			setClaims(c, claims)
		}
		RefreshIfNeeded(c, claims, secret, secureCookie)
		c.Next()
	}
}

// ValidateCurrentUser 用数据库中的当前身份替换 token 快照，由 identity 模块查询用户后调用。
func ValidateCurrentUser(c *gin.Context, userID int, email, role string) {
	c.Set(contextUserID, userID)
	c.Set("email", email)
	c.Set("role", role)
	c.Set(contextIdentityValidated, true)
}

// InvalidateCurrentUser 防止局部认证中间件恢复一个签名仍有效、但用户已删除或无法读取的 token。
func InvalidateCurrentUser(c *gin.Context) {
	c.Set(contextUserID, 0)
	c.Set("email", "")
	c.Set("role", "")
	c.Set(contextIdentityValidated, false)
}

func identityValidation(c *gin.Context) (bool, bool) {
	value, checked := c.Get(contextIdentityValidated)
	validated, _ := value.(bool)
	return validated, checked
}

func setClaims(c *gin.Context, claims Claims) {
	c.Set(contextUserID, claims.UserID)
	c.Set("email", claims.Email)
	c.Set("role", claims.Role)
	c.Set(contextClaims, claims)
}

// ClaimsFromContext 返回本次请求已解析的 token claims。滑动续期需要 Issued 和
// Expiry 才能判断是否过半，而这两个字段不在 user_id/email/role 这几个上下文键里。
func ClaimsFromContext(c *gin.Context) (Claims, bool) {
	value, found := c.Get(contextClaims)
	if !found {
		return Claims{}, false
	}
	claims, ok := value.(Claims)
	return claims, ok
}

func contextString(c *gin.Context, key string) string {
	value, _ := c.Get(key)
	text, _ := value.(string)
	return text
}

func rejectUnauthenticated(c *gin.Context) {
	if strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.Redirect(http.StatusFound, "/auth/login?redirect="+c.Request.URL.Path)
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
	}
	c.Abort()
}

// RefreshIfNeeded 在 token 寿命过半后重新签发，实现滑动续期；未过半时不写 Cookie，
// 避免每个请求都产生一次签名和 Set-Cookie。同一请求内只会续期一次，因此全局中间件
// 和路由级 Require 同时调用也不会重复下发。
//
// 调用方必须传入权威的 Email 和 Role。直接沿用 token 里的旧快照会把已经变更的邮箱
// 或已被降权的角色再延长一个完整周期。
func RefreshIfNeeded(c *gin.Context, claims Claims, secret string, secure bool) {
	if claims.Issued <= 0 || claims.Expiry <= claims.Issued {
		return
	}
	if renewed, _ := c.Get(contextSessionRenewed); renewed == true {
		return
	}
	total := time.Duration(claims.Expiry-claims.Issued) * time.Second
	if time.Since(time.Unix(claims.Issued, 0)) <= total/2 {
		return
	}
	now := time.Now()
	claims.Issued = now.Unix()
	claims.Expiry = now.Add(total).Unix()
	token, err := Sign(claims, secret)
	if err != nil {
		return
	}
	c.Set(contextSessionRenewed, true)
	c.Set(contextClaims, claims)
	http.SetCookie(c.Writer, &http.Cookie{Name: "token", Value: token, Path: "/", MaxAge: int(total.Seconds()), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func UserID(c *gin.Context) int {
	value, found := c.Get(contextUserID)
	if !found {
		return 0
	}
	userID, _ := value.(int)
	return userID
}

func Extract(request *http.Request, secret string, now time.Time) (Claims, error) {
	token := ""
	if cookie, err := request.Cookie("token"); err == nil {
		token = cookie.Value
	} else if authorization := request.Header.Get("Authorization"); strings.HasPrefix(authorization, "Bearer ") {
		token = strings.TrimPrefix(authorization, "Bearer ")
	}
	return Parse(token, secret, now)
}

func Parse(token, secret string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || secret == "" {
		return Claims{}, errors.New("malformed token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, errors.New("invalid token header")
	}
	var header struct {
		Algorithm string `json:"alg"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "HS256" {
		return Claims{}, errors.New("unsupported token algorithm")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, expected) {
		return Claims{}, errors.New("invalid token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("invalid token payload")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var raw map[string]any
	if decoder.Decode(&raw) != nil {
		return Claims{}, errors.New("invalid token claims")
	}
	claims := Claims{Email: stringClaim(raw["email"]), Role: stringClaim(raw["role"])}
	claims.UserID, err = intClaim(raw["user_id"])
	if err != nil || claims.UserID <= 0 {
		return Claims{}, errors.New("invalid user claim")
	}
	claims.Expiry, err = int64Claim(raw["exp"])
	if err != nil || claims.Expiry <= now.Unix() {
		return Claims{}, errors.New("expired token")
	}
	claims.Issued, _ = int64Claim(raw["iat"])
	if notBefore, parseErr := int64Claim(raw["nbf"]); parseErr == nil && notBefore > now.Unix() {
		return Claims{}, errors.New("token not active")
	}
	return claims, nil
}

func Sign(claims Claims, secret string) (string, error) {
	if claims.UserID <= 0 || claims.Expiry == 0 || secret == "" {
		return "", errors.New("invalid claims")
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func stringClaim(value any) string {
	text, _ := value.(string)
	return text
}

func intClaim(value any) (int, error) {
	parsed, err := int64Claim(value)
	return int(parsed), err
}

func int64Claim(value any) (int64, error) {
	switch typed := value.(type) {
	case json.Number:
		return typed.Int64()
	case float64:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, errors.New("claim is not a number")
	}
}
