package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/utils"
)

// ==================== 分类过滤管理 ====================

// AdminCategory 分类过滤管理页面
func (h *Handler) AdminCategory(c *gin.Context) {
	filters, err := h.Repos.CategoryFilter.ListAll()
	if err != nil {
		filters = []*model.CategoryFilter{}
	}

	c.HTML(http.StatusOK, "admin_category.html", h.RenderData(c, gin.H{
		"Title":   "分类过滤管理 - Moovie影牛",
		"Filters": filters,
	}))
}

// AdminCategoryCreate 添加分类关键词
func (h *Handler) AdminCategoryCreate(c *gin.Context) {
	keyword := c.PostForm("keyword")
	if keyword == "" {
		utils.BadRequest(c, "关键词不能为空")
		return
	}

	filter := &model.CategoryFilter{
		Keyword: keyword,
	}

	if err := h.Repos.CategoryFilter.Create(filter); err != nil {
		utils.InternalServerError(c, "创建失败: "+err.Error())
		return
	}

	utils.Success(c, filter)
}

// AdminCategoryDelete 删除分类关键词
func (h *Handler) AdminCategoryDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		utils.BadRequest(c, "无效的 ID")
		return
	}

	if err := h.Repos.CategoryFilter.Delete(uint(id)); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	utils.Success(c, nil)
}
