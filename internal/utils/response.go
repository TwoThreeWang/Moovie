package utils

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// HashIP 对 IP 地址进行哈希处理（用于匿名统计）
func HashIP(ip string) string {
	hash := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(hash[:8]) // 只取前8字节，足够用于统计
}

// Response 统一API响应结构
type Response struct {
	Code    int         `json:"code"`    // 状态码
	Message string      `json:"message"` // 消息
	Data    interface{} `json:"data"`    // 数据
	Success bool        `json:"success"` // 是否成功
}

// Success 返回成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(200, Response{
		Code:    200,
		Message: "success",
		Data:    data,
		Success: true,
	})
}

// SuccessWithMessage 返回成功响应并自定义消息
func SuccessWithMessage(c *gin.Context, message string, data interface{}) {
	c.JSON(200, Response{
		Code:    200,
		Message: message,
		Data:    data,
		Success: true,
	})
}

// Error 返回错误响应
func Error(c *gin.Context, code int, message string) {
	c.JSON(code, Response{
		Code:    code,
		Message: message,
		Data:    nil,
		Success: false,
	})
}

// BadRequest 返回400错误
func BadRequest(c *gin.Context, message string) {
	Error(c, 400, message)
}

// Unauthorized 返回401错误
func Unauthorized(c *gin.Context, message string) {
	if message == "" {
		message = "未登录"
	}
	Error(c, 401, message)
}

// InternalServerError 返回500错误
func InternalServerError(c *gin.Context, message string) {
	if message == "" {
		message = "服务器内部错误"
	}
	Error(c, 500, message)
}

// NotFound 返回404错误
func NotFound(c *gin.Context, message string) {
	if message == "" {
		message = "资源不存在"
	}
	Error(c, 404, message)
}
