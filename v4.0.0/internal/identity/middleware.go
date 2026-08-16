package identity

import (
	"context"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/gin-gonic/gin"
)

const UserInfoContextKey = "user_info"

type UserReader interface {
	FindByID(ctx context.Context, id int) (*User, error)
}

// LoadUser 保留旧站行为，让每个服务端渲染页面都能在共享 layout 中显示当前用户。
// token 无效或用户已删除时继续按匿名访问处理。
//
// 它同时承担滑动续期。续期放在这里而不是 auth.Optional，是因为这一层才同时具备三个
// 条件：已经跳过静态资源和图片代理（给它们加 Set-Cookie 会破坏 CDN 缓存）、已经确认
// 用户在库中真实存在、拿得到库中最新的邮箱和角色。只挂在 auth.Require 上则覆盖不到
// 首页、搜索、详情、播放这些占绝大多数的浏览路径。
func LoadUser(store UserReader, secret string, secureCookie bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil || strings.HasPrefix(c.Request.URL.Path, "/static/") || strings.HasPrefix(c.Request.URL.Path, "/api/proxy/image/") {
			c.Next()
			return
		}
		if userID := auth.UserID(c); userID > 0 {
			if user, err := store.FindByID(c.Request.Context(), userID); err == nil && user != nil {
				auth.ValidateCurrentUser(c, user.ID, user.Email, user.Role)
				c.Set(UserInfoContextKey, user)
				if claims, ok := auth.ClaimsFromContext(c); ok {
					claims.Email, claims.Role = user.Email, user.Role
					auth.RefreshIfNeeded(c, claims, secret, secureCookie)
				}
			} else {
				auth.InvalidateCurrentUser(c)
			}
		}
		c.Next()
	}
}
