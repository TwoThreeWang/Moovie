package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Claims JWT 声明
type Claims struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// RequireAuth 必须登录中间件
func RequireAuth(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := extractClaims(c, jwtSecret)
		if err != nil {
			// 如果是页面请求，重定向到登录页
			if strings.Contains(c.GetHeader("Accept"), "text/html") {
				c.Redirect(http.StatusFound, "/auth/login?redirect="+c.Request.URL.Path)
				c.Abort()
				return
			}
			// API 请求返回 401
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
			c.Abort()
			return
		}

		// 将用户信息存入上下文
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)

		// 滑动续期逻辑：如果 Token 过期时间消耗超过一半，则刷新
		if shouldRefresh(claims) {
			newToken, err := GenerateToken(claims.UserID, claims.Email, claims.Role, jwtSecret, claims.RegisteredClaims.ExpiresAt.Sub(claims.RegisteredClaims.IssuedAt.Time))
			if err == nil {
				// 设置新的 Cookie，保持与 main.go 中一致的安全属性
				c.SetCookie("token", newToken, int(claims.RegisteredClaims.ExpiresAt.Sub(claims.RegisteredClaims.IssuedAt.Time).Seconds()), "/", "", false, true)
			}
		}

		c.Next()
	}
}

// OptionalAuth 可选登录中间件（不强制要求登录）
func OptionalAuth(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := extractClaims(c, jwtSecret)
		if err == nil {
			c.Set("user_id", claims.UserID)
			c.Set("email", claims.Email)
			c.Set("role", claims.Role)

			// 滑动续期逻辑
			if shouldRefresh(claims) {
				newToken, err := GenerateToken(claims.UserID, claims.Email, claims.Role, jwtSecret, claims.RegisteredClaims.ExpiresAt.Sub(claims.RegisteredClaims.IssuedAt.Time))
				if err == nil {
					c.SetCookie("token", newToken, int(claims.RegisteredClaims.ExpiresAt.Sub(claims.RegisteredClaims.IssuedAt.Time).Seconds()), "/", "", false, true)
				}
			}
		}
		c.Next()
	}
}

// RequireAdmin 管理员权限中间件
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists || role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// extractClaims 从 Cookie 或 Header 中提取 JWT Claims
func extractClaims(c *gin.Context, jwtSecret string) (*Claims, error) {
	var tokenString string

	// 优先从 Cookie 获取
	if cookie, err := c.Cookie("token"); err == nil {
		tokenString = cookie
	} else {
		// 从 Authorization Header 获取
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if tokenString == "" {
		return nil, jwt.ErrTokenMalformed
	}

	// 解析 Token
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}

	return claims, nil
}

// GetUserID 从上下文获取用户 ID（未登录返回 0）
func GetUserID(c *gin.Context) int {
	if userID, exists := c.Get("user_id"); exists {
		return userID.(int)
	}
	return 0
}

// GetUserIDPtr 从上下文获取用户 ID 指针（未登录返回 nil）
func GetUserIDPtr(c *gin.Context) *int {
	if userID, exists := c.Get("user_id"); exists {
		id := userID.(int)
		return &id
	}
	return nil
}

// GenerateToken 生成 JWT Token
func GenerateToken(userID int, email, role, jwtSecret string, expiry time.Duration) (string, error) {
	claims := &Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(jwtSecret))
}

// shouldRefresh 判断是否需要刷新 Token
// 逻辑：如果已经消耗了总有效期的 50% 以上，则建议刷新
func shouldRefresh(claims *Claims) bool {
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return false
	}

	totalDuration := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	elapsedDuration := time.Since(claims.IssuedAt.Time)

	// 如果消耗超过 50%
	return elapsedDuration > totalDuration/2
}
