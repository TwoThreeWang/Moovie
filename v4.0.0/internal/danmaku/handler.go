package danmaku

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

// Handler 提供弹幕的读写接口。
type Handler struct {
	config  config.Config
	service *Service
}

// NewHandler 创建弹幕处理器。
func NewHandler(cfg config.Config, service *Service) *Handler {
	return &Handler{config: cfg, service: service}
}

// Register 注册路由：读弹幕不需要登录，发弹幕需要登录。
func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/api/danmaku", handler.list)
	router.POST("/api/danmaku", auth.Optional(handler.config.AppSecret), handler.send)
}

// list 返回某一集的弹幕（上游 + 站内合并）。
func (handler *Handler) list(c *gin.Context) {
	items := handler.service.List(c.Request.Context(), c.Query("title"), c.Query("episode"), c.ClientIP())
	if items == nil {
		items = []Item{}
	}
	c.JSON(http.StatusOK, items)
}

// send 发送弹幕，请求体限制 16KB，各种拒绝原因映射成对应的中文提示。
func (handler *Handler) send(c *gin.Context) {
	userID := auth.UserID(c)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后再发送弹幕"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var input SendInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	err := handler.service.Send(c.Request.Context(), userID, input)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"ok": true})
	case errors.Is(err, errParameters):
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
	case errors.Is(err, errEmptyText):
		c.JSON(http.StatusBadRequest, gin.H{"error": "弹幕内容不能为空"})
	case errors.Is(err, errLongText):
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("弹幕最多 %d 个字", maxTextLength)})
	case errors.Is(err, ErrRateLimited):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "发送太频繁了，歇一会儿再发"})
	case errors.Is(err, ErrDuplicate):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "刚发过一样的内容了"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送失败，请稍后重试"})
	}
}
