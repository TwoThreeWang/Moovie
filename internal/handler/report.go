package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ReportLoadSpeed 上报视频加载速度
type ReportLoadSpeedRequest struct {
	SourceKey string `json:"source_key" binding:"required"` // 来源站点Key
	VodID     string `json:"vod_id" binding:"required"`     // 视频ID
	LoadTime  int    `json:"load_time" binding:"required"`  // 加载耗时(毫秒)
	Status    string `json:"status" binding:"required"`     // success/failed
	Reason    string `json:"reason,omitempty"`              // 失败原因
}

// ReportLoadSpeed 处理加载速度上报
func (h *Handler) ReportLoadSpeed(c *gin.Context) {
	var req ReportLoadSpeedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.SourceKey == "" || req.VodID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.LoadTime <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	// 增量平均算法更新数据库
	if req.Status == "success" {
		// 新平均值 = (旧平均值 * 旧样本数 + 本次耗时) / (旧样本数 + 1)
		err := h.Repos.Movie.UpdateLoadSpeedBySource(req.SourceKey, req.VodID, req.LoadTime)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}
	} else {
		// 失败只增加失败计数
		err := h.Repos.Movie.IncrementFailedCountBySource(req.SourceKey, req.VodID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "上报成功"})
}
