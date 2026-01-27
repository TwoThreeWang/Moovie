package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/service"
	"github.com/user/moovie/internal/utils"
)

// SimilarMoviesWithReasonsHTMX 返回带推荐理由的相似电影 (HTMX)
func (h *Handler) SimilarMoviesWithReasonsHTMX(c *gin.Context) {
	doubanID := c.Param("douban_id")
	if doubanID == "" {
		c.String(http.StatusBadRequest, "豆瓣ID不能为空")
		return
	}

	// 获取带推荐理由的相似电影
	similarMovies, _, err := h.RecommendationService.FindSimilarWithReasons(doubanID, 8)
	if err != nil {
		log.Printf("获取相似电影失败: %v", err)
		c.String(http.StatusInternalServerError, "获取相似电影失败")
		return
	}

	c.HTML(http.StatusOK, "partials/similar_movies_with_reasons.html", gin.H{
		"SimilarMovies": similarMovies,
	})
}

// RecommendationsPage 相似电影推荐页面
func (h *Handler) RecommendationsPage(c *gin.Context) {
	doubanID := c.Param("douban_id")
	if doubanID == "" {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "页面未找到 - Moovie",
		}))
		return
	}

	const limit = 12

	type recommendationsCachePayload struct {
		SimilarMovies []service.SimilarMovieWithReason
		SourceMovie   *model.Movie
	}

	cacheKey := fmt.Sprintf("recommendations:%s:%d", doubanID, limit)
	var similarMovies []service.SimilarMovieWithReason
	var sourceMovie *model.Movie

	if cached, found := utils.CacheGet(cacheKey); found {
		if payload, ok := cached.(recommendationsCachePayload); ok {
			similarMovies = payload.SimilarMovies
			sourceMovie = payload.SourceMovie
		}
	}

	if sourceMovie == nil {
		var err error
		similarMovies, sourceMovie, err = h.RecommendationService.FindSimilarWithReasons(doubanID, limit)
		if err != nil || sourceMovie == nil {
			if err != nil {
				log.Printf("获取相似电影失败: %v", err)
			}
			c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
				"Title": "电影未找到 - " + h.Config.SiteName,
			}))
			return
		}

		utils.CacheSet(cacheKey, recommendationsCachePayload{
			SimilarMovies: similarMovies,
			SourceMovie:   sourceMovie,
		}, 1*time.Hour)
	}

	// SEO: 标题
	titleTag := fmt.Sprintf("类似《%s》的电影推荐_和《%s》差不多的电影 - %s", sourceMovie.Title, sourceMovie.Title, h.Config.SiteName)

	// 导演
	directorNames := ""
	if sourceMovie.Directors != "" {
		var ds []model.Person
		_ = json.Unmarshal([]byte(sourceMovie.Directors), &ds)
		names := make([]string, 0, len(ds))
		for _, d := range ds {
			if d.Name != "" {
				names = append(names, d.Name)
			}
		}
		if len(names) > 0 {
			directorNames = strings.Join(names, "、")
		}
	}
	// 类型
	primaryGenre := ""
	if sourceMovie.Genres != "" {
		parts := strings.Split(sourceMovie.Genres, ",")
		if len(parts) > 0 {
			primaryGenre = strings.TrimSpace(parts[0])
		}
	}
	// 示例片名
	sim1 := ""
	sim2 := ""
	if len(similarMovies) > 0 {
		sim1 = similarMovies[0].Movie.Title
	}
	if len(similarMovies) > 1 {
		sim2 = similarMovies[1].Movie.Title
	}
	// 组装描述
	descParts := []string{
		fmt.Sprintf("为您精选多部类似《%s》的电影。", sourceMovie.Title),
	}
	reasonCore := "基于剧情内核"
	if directorNames != "" {
		reasonCore += "、导演" + directorNames + "风格"
	}
	if primaryGenre != "" {
		reasonCore += "及" + primaryGenre + "题材"
	}
	reasonCore += "，结合向量相似度，为您推荐"
	reco := ""
	if sim1 != "" && sim2 != "" {
		reco = fmt.Sprintf("《%s》、《%s》等高相关佳作。", sim1, sim2)
	} else if sim1 != "" {
		reco = fmt.Sprintf("《%s》等高相关佳作。", sim1)
	}
	descParts = append(descParts, reasonCore, reco)
	description := strings.TrimSpace(strings.Join(descParts, " "))

	// Canonical
	canonical := fmt.Sprintf("%s/similar/%s", h.Config.SiteUrl, sourceMovie.DoubanID)

	c.HTML(http.StatusOK, "recommendations.html", h.RenderData(c, gin.H{
		"Title":         titleTag,
		"Description":   description,
		"Canonical":     canonical,
		"SourceMovie":   sourceMovie,
		"SimilarMovies": similarMovies,
		"PrimaryGenre":  primaryGenre,
	}))
}
