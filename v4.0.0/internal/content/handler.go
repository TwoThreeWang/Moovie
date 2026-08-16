package content

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

const verificationBody = "monoo_verify_6ec090aa1f53018a12c8030087e10cc6"

type Handler struct {
	config          config.Config
	sitemapProvider SitemapMovieProvider
	sitemapMu       sync.Mutex
	sitemapBody     []byte
	sitemapExpires  time.Time
}

func NewHandler(cfg config.Config, sitemapProvider SitemapMovieProvider) *Handler {
	return &Handler{config: cfg, sitemapProvider: sitemapProvider}
}

func (h *Handler) Register(router *gin.Engine, staticDir string) {
	router.Static("/static", staticDir)

	router.GET("/", h.page("home.html", h.config.SiteName+" - 发现你的下一部电影"))
	router.GET("/about", h.page("about.html", "关于 - "+h.config.SiteName))
	router.GET("/advertise", h.page("advertise.html", "广告合作 - "+h.config.SiteName))
	router.GET("/changelog", h.page("changelog.html", "更新记录 - "+h.config.SiteName))
	router.GET("/dmca", h.page("dmca.html", "DMCA 声明 - "+h.config.SiteName))
	router.GET("/privacy", h.page("privacy.html", "隐私政策 - "+h.config.SiteName))
	router.GET("/terms", h.page("terms.html", "服务协议 - "+h.config.SiteName))
	router.GET("/copyright-restricted", h.copyrightRestricted)
	router.GET("/sitemap.xml", h.sitemap)
	router.GET("/robots.txt", h.robots)
	router.GET("/monoo-verify.txt", h.verification)

	router.NoRoute(h.notFound)
}

func (h *Handler) page(templateName, title string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, templateName, newViewModel(c, h.config, Metadata{Title: title}))
	}
}

func (h *Handler) copyrightRestricted(c *gin.Context) {
	view := newViewModel(c, h.config, Metadata{Title: "版权限制 - " + h.config.SiteName})
	view.MovieTitle = c.Query("title")
	c.HTML(http.StatusOK, "copyright_restricted.html", view)
}

func (h *Handler) sitemap(c *gin.Context) {
	h.sitemapMu.Lock()
	body := h.sitemapBody
	if len(body) == 0 || time.Now().After(h.sitemapExpires) {
		var err error
		body, err = buildSitemap(c.Request.Context(), h.config.SiteURL, h.sitemapProvider)
		if err == nil {
			h.sitemapBody = body
			h.sitemapExpires = time.Now().Add(10 * time.Minute)
		}
		if err != nil {
			h.sitemapMu.Unlock()
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}
	h.sitemapMu.Unlock()
	c.Header("Cache-Control", "public, max-age=300, stale-while-revalidate=3600")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", body)
}

func (h *Handler) robots(c *gin.Context) {
	baseURL := strings.TrimRight(h.config.SiteURL, "/")
	body := "User-agent: *\n" +
		"Disallow: /admin/\n" +
		"Disallow: /auth/\n" +
		"Disallow: /dashboard/\n" +
		"Disallow: /api/proxy/image/\n" +
		"Disallow: /api/\n\n" +
		"Sitemap: " + baseURL + "/sitemap.xml\n"
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body))
}

func (h *Handler) verification(c *gin.Context) {
	c.String(http.StatusOK, verificationBody)
}

func (h *Handler) notFound(c *gin.Context) {
	c.HTML(http.StatusNotFound, "404.html", newViewModel(c, h.config, Metadata{Title: "页面未找到 - Moovie影牛"}))
}
