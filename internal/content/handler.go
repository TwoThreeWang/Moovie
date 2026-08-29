// Package content 提供静态页面和 SEO 相关端点：首页、关于、隐私政策等固定页面，
// 以及 sitemap.xml、robots.txt、站点验证文件和 404 页。
// 本包不碰数据库，唯一的数据来源是 sitemap 需要的影片列表。
package content

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

// verificationBody 是站长平台的站点归属验证串。
const verificationBody = "monoo_verify_6ec090aa1f53018a12c8030087e10cc6"

type sitemapCacheEntry struct {
	body    []byte
	expires time.Time
}

// Handler 负责静态页面，并在内存里缓存 sitemap。
type Handler struct {
	config          config.Config
	sitemapProvider SitemapMovieProvider
	sitemapMu       sync.Mutex
	sitemapCache    map[string]sitemapCacheEntry
}

// NewHandler 创建静态页面处理器。
func NewHandler(cfg config.Config, sitemapProvider SitemapMovieProvider) *Handler {
	return &Handler{config: cfg, sitemapProvider: sitemapProvider, sitemapCache: make(map[string]sitemapCacheEntry)}
}

// Register 注册静态资源目录、各固定页面和 404 兜底。
// NoRoute 必须由本包注册，其他包不要再注册。
func (h *Handler) Register(router *gin.Engine, staticDir string) {
	router.Static("/static", staticDir)

	router.GET("/", h.page("home.html", h.config.SiteName+" - 发现你的下一部电影", "/"))
	router.GET("/about", h.page("about.html", "关于 - "+h.config.SiteName, "/about"))
	router.GET("/advertise", h.page("advertise.html", "广告合作 - "+h.config.SiteName, "/advertise"))
	router.GET("/changelog", h.page("changelog.html", "更新记录 - "+h.config.SiteName, "/changelog"))
	router.GET("/dmca", h.page("dmca.html", "DMCA 声明 - "+h.config.SiteName, "/dmca"))
	router.GET("/privacy", h.page("privacy.html", "隐私政策 - "+h.config.SiteName, "/privacy"))
	router.GET("/terms", h.page("terms.html", "服务协议 - "+h.config.SiteName, "/terms"))
	router.GET("/copyright-restricted", h.copyrightRestricted)
	router.GET("/sitemap.xml", h.sitemapIndex)
	router.GET("/sitemaps/static.xml", h.staticSitemap)
	router.GET("/sitemaps/:kind/:page", h.mediaSitemap)
	router.GET("/robots.txt", h.robots)
	router.GET("/monoo-verify.txt", h.verification)

	router.NoRoute(h.notFound)
}

// page 把「渲染某个模板 + 固定标题」包成一个 Handler。
func (h *Handler) page(templateName, title, path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, templateName, newViewModel(c, h.config, Metadata{Title: title, Canonical: canonicalURL(h.config.SiteURL, path)}))
	}
}

// copyrightRestricted 渲染版权限制提示页。
func (h *Handler) copyrightRestricted(c *gin.Context) {
	view := newViewModel(c, h.config, Metadata{Title: "版权限制 - " + h.config.SiteName, Robots: "noindex, follow"})
	view.MovieTitle = c.Query("title")
	c.HTML(http.StatusOK, "copyright_restricted.html", view)
}

func (h *Handler) sitemapIndex(c *gin.Context) {
	h.serveSitemap(c, "index", 10*time.Minute, "public, max-age=300, stale-while-revalidate=3600", func() ([]byte, bool, error) {
		body, err := buildSitemapIndex(c.Request.Context(), h.config.SiteURL, h.sitemapProvider)
		return body, true, err
	})
}

func (h *Handler) staticSitemap(c *gin.Context) {
	h.serveSitemap(c, "static", 24*time.Hour, "public, max-age=86400", func() ([]byte, bool, error) {
		body, err := buildStaticSitemap(h.config.SiteURL)
		return body, true, err
	})
}

func (h *Handler) mediaSitemap(c *gin.Context) {
	kind := SitemapKind(c.Param("kind"))
	if kind != SitemapMovies && kind != SitemapSimilar {
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusNotFound)
		return
	}
	pageText := strings.TrimSuffix(c.Param("page"), ".xml")
	page, err := strconv.Atoi(pageText)
	if err != nil || page < 1 || pageText+".xml" != c.Param("page") {
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusNotFound)
		return
	}
	key := string(kind) + ":" + strconv.Itoa(page)
	h.serveSitemap(c, key, time.Hour, "public, max-age=3600", func() ([]byte, bool, error) {
		return buildMediaSitemap(c.Request.Context(), h.config.SiteURL, h.sitemapProvider, kind, page)
	})
}

func (h *Handler) serveSitemap(c *gin.Context, key string, ttl time.Duration, cacheControl string, generate func() ([]byte, bool, error)) {
	h.sitemapMu.Lock()
	entry, cached := h.sitemapCache[key]
	if !cached || time.Now().After(entry.expires) {
		body, found, err := generate()
		if err != nil || !found {
			h.sitemapMu.Unlock()
			c.Header("Cache-Control", "no-store")
			if err != nil {
				c.Status(http.StatusInternalServerError)
			} else {
				c.Status(http.StatusNotFound)
			}
			return
		}
		entry = sitemapCacheEntry{body: body, expires: time.Now().Add(ttl)}
		h.sitemapCache[key] = entry
	}
	h.sitemapMu.Unlock()
	c.Header("Cache-Control", cacheControl)
	c.Data(http.StatusOK, "application/xml; charset=utf-8", entry.body)
}

// robots 返回 robots.txt，屏蔽后台、登录和接口路径。
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

// verification 返回站点验证文件。
func (h *Handler) verification(c *gin.Context) {
	c.String(http.StatusOK, verificationBody)
}

// notFound 渲染 404 页面。
func (h *Handler) notFound(c *gin.Context) {
	c.HTML(http.StatusNotFound, "404.html", newViewModel(c, h.config, Metadata{Title: "页面未找到 - Moovie影牛", Robots: "noindex, follow"}))
}
