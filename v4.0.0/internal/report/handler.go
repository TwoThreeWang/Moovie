package report

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

const publicPageSize = 24

type PublicUserStore interface {
	FindByID(ctx context.Context, userID int) (*identity.User, error)
}

type Handler struct {
	config  config.Config
	users   PublicUserStore
	library library.Store
	reports Store
	service *Service
}

func NewHandler(cfg config.Config, users PublicUserStore, libraryStore library.Store, reports Store, service *Service) *Handler {
	return &Handler{config: cfg, users: users, library: libraryStore, reports: reports, service: service}
}

func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/user/:user_id", optional, handler.publicProfile)
	router.GET("/user/:user_id/monthly/:year_month", optional, handler.publicMonthly)
	router.GET("/api/htmx/public/:user_id/wish", optional, handler.publicWish)
	router.GET("/api/htmx/public/:user_id/watched", optional, handler.publicWatched)
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.POST("/admin/monthly-report/generate", require, requireAdmin, handler.adminGenerate)
}

func (handler *Handler) publicProfile(c *gin.Context) {
	user := handler.publicUser(c)
	if user == nil {
		handler.notFound(c, "页面未找到 - "+handler.config.SiteName)
		return
	}
	wish, _ := handler.library.ListByUser(c.Request.Context(), user.ID, library.StatusWish, publicPageSize, 0)
	watched, _ := handler.library.ListByUser(c.Request.Context(), user.ID, library.StatusWatched, publicPageSize, 0)
	wishCount, _ := handler.library.CountByUser(c.Request.Context(), user.ID, library.StatusWish)
	watchedCount, _ := handler.library.CountByUser(c.Request.Context(), user.ID, library.StatusWatched)
	average, ratedCount, _ := handler.library.AvgRatingByUser(c.Request.Context(), user.ID)
	reports, _ := handler.reports.ListByUser(c.Request.Context(), user.ID, 6, 0)
	canonical := fmt.Sprintf("%s/user/%d", handler.config.SiteURL, user.ID)
	c.HTML(http.StatusOK, "share.html", platformweb.NewData(c, handler.config,
		platformweb.Metadata{Title: user.Username + " 的观影记录 - " + handler.config.SiteName, Canonical: canonical}, gin.H{
			"User": user, "WishList": wish, "WatchedList": watched, "WishCount": wishCount, "WatchedCount": watchedCount,
			"WishHasMore": wishCount > len(wish), "WishNextPage": 2,
			"WatchedHasMore": watchedCount > len(watched), "WatchedNextPage": 2,
			"AvgRating": average, "RatedCount": ratedCount, "MonthlyReports": reports, "Canonical": canonical,
		}))
}

func (handler *Handler) publicMonthly(c *gin.Context) {
	user := handler.publicUser(c)
	if user == nil {
		handler.notFound(c, "页面未找到 - "+handler.config.SiteName)
		return
	}
	yearMonth := c.Param("year_month")
	start, end, err := monthRange(yearMonth)
	if err != nil {
		handler.notFound(c, "未找到报告 - "+handler.config.SiteName)
		return
	}
	report, err := handler.reports.GetByUserAndMonth(c.Request.Context(), user.ID, yearMonth)
	if err != nil || report == nil {
		handler.notFound(c, "未找到报告 - "+handler.config.SiteName)
		return
	}
	genreStats := []GenreStat{}
	posterWall := []PosterWallItem{}
	if report.GenreStats != "" {
		_ = json.Unmarshal([]byte(report.GenreStats), &genreStats)
	}
	if report.PosterWall != "" {
		_ = json.Unmarshal([]byte(report.PosterWall), &posterWall)
	}
	extra := report.WatchedCount - len(posterWall)
	if extra < 0 {
		extra = 0
	}
	monthlyMovies, _ := handler.library.ListByUserAndDateRange(c.Request.Context(), user.ID, library.StatusWatched, start, end)
	shareURL := fmt.Sprintf("%s/user/%d/monthly/%s", handler.config.SiteURL, user.ID, yearMonth)
	profileURL := fmt.Sprintf("%s/user/%d", handler.config.SiteURL, user.ID)
	siteDomain := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(handler.config.SiteURL, "https://"), "http://"), "/")
	overlap, showOverlap := 0, false
	if viewerID := auth.UserID(c); viewerID > 0 && viewerID != user.ID {
		if count, overlapErr := handler.library.CountOverlapWatched(c.Request.Context(), viewerID, user.ID); overlapErr == nil {
			overlap, showOverlap = count, true
		}
	}
	c.HTML(http.StatusOK, "share_monthly.html", platformweb.NewData(c, handler.config,
		platformweb.Metadata{Title: user.Username + " " + yearMonth + " 月度观影小记 - " + handler.config.SiteName, Canonical: shareURL}, gin.H{
			"User": user, "Report": report, "GenreStats": genreStats, "PosterWall": posterWall,
			"PosterWallExtra": extra, "MonthlyMovies": monthlyMovies, "Canonical": shareURL,
			"ProfileURL": profileURL, "SiteDomain": siteDomain, "ShowOverlap": showOverlap, "OverlapCount": overlap,
		}))
}

func (handler *Handler) publicWish(c *gin.Context)    { handler.publicList(c, library.StatusWish) }
func (handler *Handler) publicWatched(c *gin.Context) { handler.publicList(c, library.StatusWatched) }

func (handler *Handler) publicList(c *gin.Context, status string) {
	user := handler.publicUser(c)
	if user == nil {
		c.Status(http.StatusNotFound)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * publicPageSize
	records, _ := handler.library.ListByUser(c.Request.Context(), user.ID, status, publicPageSize, offset)
	count, _ := handler.library.CountByUser(c.Request.Context(), user.ID, status)
	data := gin.H{"UserID": user.ID}
	if status == library.StatusWish {
		data["WishList"], data["WishHasMore"], data["WishNextPage"] = records, offset+len(records) < count, page+1
		c.HTML(http.StatusOK, "partials/public_wish_grid.html", data)
		return
	}
	data["WatchedList"], data["WatchedHasMore"], data["WatchedNextPage"] = records, offset+len(records) < count, page+1
	c.HTML(http.StatusOK, "partials/public_watched_grid.html", data)
}

func (handler *Handler) publicUser(c *gin.Context) *identity.User {
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		return nil
	}
	user, err := handler.users.FindByID(c.Request.Context(), userID)
	if err != nil || user == nil || !user.IsPublic {
		return nil
	}
	return user
}

func (handler *Handler) adminGenerate(c *gin.Context) {
	userID, err := strconv.Atoi(c.PostForm("user_id"))
	if err != nil || userID <= 0 {
		apiError(c, http.StatusBadRequest, "请输入有效的用户ID")
		return
	}
	yearMonth := strings.TrimSpace(c.PostForm("year_month"))
	if yearMonth == "" {
		yearMonth = time.Now().AddDate(0, -1, 0).Format("2006-01")
	}
	start, end, err := monthRange(yearMonth)
	if err != nil {
		apiError(c, http.StatusBadRequest, "月份格式应为 2026-07")
		return
	}
	counts, err := handler.library.CountWatchedByAllUsersInRange(c.Request.Context(), start, end)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "统计全站分布失败")
		return
	}
	if err := handler.service.Generate(c.Request.Context(), userID, yearMonth, counts); err != nil {
		apiError(c, http.StatusInternalServerError, "生成失败: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "success", "data": gin.H{
		"user_id": userID, "year_month": yearMonth, "url": fmt.Sprintf("/user/%d/monthly/%s", userID, yearMonth),
	}, "success": true})
}

func requireAdmin(c *gin.Context) {
	if role, exists := c.Get("role"); !exists || role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
		c.Abort()
		return
	}
	c.Next()
}

func apiError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"code": status, "message": message, "data": nil, "success": false})
}

func (handler *Handler) notFound(c *gin.Context, title string) {
	c.HTML(http.StatusNotFound, "404.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: title}, gin.H{"Path": c.Request.URL.Path}))
}
