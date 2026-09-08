package collection

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

// listPageSize 是片单索引页每页数量。
const listPageSize = 24

// Handler 提供片单的公开页面和后台管理。
type Handler struct {
	config config.Config
	store  Store
}

// NewHandler 创建片单处理器。
func NewHandler(cfg config.Config, store Store) *Handler {
	return &Handler{config: cfg, store: store}
}

// Register 注册片单路由。公开页对游客开放（未发布的片单只有管理员看得到），
// 后台三条和 internal/admin 一样先过登录再过管理员校验。
func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/list", optional, handler.index)
	router.GET("/list/:slug", optional, handler.detail)

	admin := []gin.HandlerFunc{
		auth.Require(handler.config.AppSecret, handler.config.Env == "production"),
		requireAdmin,
	}
	router.GET("/admin/collections", append(admin, handler.adminPage)...)
	router.POST("/admin/collections", append(admin, handler.adminSave)...)
	router.DELETE("/admin/collections/:id", append(admin, handler.adminDelete)...)
}

// requireAdmin 是管理员校验中间件。
func requireAdmin(c *gin.Context) {
	if !isAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"code": http.StatusForbidden, "message": "需要管理员权限", "success": false})
		c.Abort()
		return
	}
	c.Next()
}

// index 渲染片单索引页。
func (handler *Handler) index(c *gin.Context) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		handler.notFound(c)
		return
	}
	count, err := handler.store.CountFeatured(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "片单暂时无法加载")
		return
	}
	if page > 1 && (count == 0 || page > (count-1)/listPageSize+1) {
		handler.notFound(c)
		return
	}
	offset := (page - 1) * listPageSize
	collections, err := handler.store.ListFeatured(c.Request.Context(), listPageSize, offset)
	if err != nil {
		c.String(http.StatusInternalServerError, "片单暂时无法加载")
		return
	}
	canonical := platformweb.CanonicalURL(handler.config.SiteURL, "/list")
	if page > 1 {
		canonical += "?page=" + strconv.Itoa(page)
	}
	if page > 1 && len(collections) == 0 {
		handler.notFound(c)
		return
	}
	c.HTML(http.StatusOK, "collections.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       "片单 - " + handler.config.SiteName,
		Description: "按主题整理的电影片单，每部都写清楚了为什么值得看。",
		Canonical:   canonical,
	}, gin.H{
		"Collections": collections, "Total": count, "Page": page,
		"HasMore": offset+len(collections) < count, "NextPage": page + 1,
	}))
}

// detail 渲染单个片单。未发布的片单只有管理员能看到，便于发布前预览。
func (handler *Handler) detail(c *gin.Context) {
	collection, err := handler.store.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		c.String(http.StatusInternalServerError, "片单暂时无法加载")
		return
	}
	if collection == nil || ((!collection.Featured || collection.ItemCount == 0) && !isAdmin(c)) {
		handler.notFound(c)
		return
	}
	items, err := handler.store.ListItems(c.Request.Context(), collection.ID)
	if err != nil {
		c.String(http.StatusInternalServerError, "片单暂时无法加载")
		return
	}
	canonical := platformweb.CanonicalURL(handler.config.SiteURL, "/list/"+collection.Slug)
	metadata := platformweb.Metadata{
		Title:       collection.Title + " - " + handler.config.SiteName,
		Description: summarize(collection.Description, 140),
		Canonical:   canonical,
	}
	elements := make([]map[string]any, 0, len(items))
	for i, item := range items {
		elements = append(elements, map[string]any{"@type": "ListItem", "position": i + 1, "name": item.Title, "url": platformweb.CanonicalURL(handler.config.SiteURL, "/movie/"+item.DoubanID), "description": item.Note})
	}
	listJSON, _ := platformweb.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "ItemList", "name": collection.Title, "description": collection.Description, "url": canonical, "numberOfItems": len(items), "itemListElement": elements})
	breadcrumbs, _ := platformweb.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": []map[string]any{
		{"@type": "ListItem", "position": 1, "name": "首页", "item": platformweb.CanonicalURL(handler.config.SiteURL, "/")},
		{"@type": "ListItem", "position": 2, "name": "片单", "item": platformweb.CanonicalURL(handler.config.SiteURL, "/list")},
		{"@type": "ListItem", "position": 3, "name": collection.Title, "item": canonical},
	}})
	metadata.JSONLD = []template.JS{listJSON, breadcrumbs}
	related, err := handler.store.ListRelated(c.Request.Context(), collection.ID, true)
	if err != nil {
		c.String(http.StatusInternalServerError, "片单暂时无法加载")
		return
	}
	// 没发布的片单是管理员在预览，不能让它进索引。
	if !collection.Featured || len(items) == 0 {
		metadata.Robots = "noindex, nofollow"
	}
	c.HTML(http.StatusOK, "collection.html", platformweb.NewData(c, handler.config, metadata, gin.H{
		"Collection": collection, "Items": items, "Canonical": canonical, "RelatedCollections": related,
	}))
}

// adminPage 渲染后台片单列表，带 id 时同时载入编辑表单。
func (handler *Handler) adminPage(c *gin.Context) {
	data := gin.H{"Notice": c.Query("notice"),
		"Error": c.Query("error"), "Unknown": c.Query("unknown")}
	if slug := c.Query("edit"); slug != "" {
		editing, err := handler.store.GetBySlug(c.Request.Context(), slug)
		if err != nil {
			c.String(http.StatusInternalServerError, "片单暂时无法加载")
			return
		}
		if editing != nil {
			items, err := handler.store.ListItems(c.Request.Context(), editing.ID)
			if err != nil {
				c.String(http.StatusInternalServerError, "片单条目暂时无法加载")
				return
			}
			related, err := handler.store.ListRelated(c.Request.Context(), editing.ID, false)
			if err != nil {
				c.String(http.StatusInternalServerError, "相关片单暂时无法加载")
				return
			}
			for _, item := range related {
				editing.Related = append(editing.Related, RelatedInput{ID: item.ID, Reason: item.Reason})
			}
			data["Editing"], data["EditingItems"] = editing, FormatItems(items)
		}
	}
	handler.renderAdmin(c, http.StatusOK, data)
}

// renderAdmin 让保存失败时保留表单；即使列表查询失败也不丢掉输入。
func (handler *Handler) renderAdmin(c *gin.Context, status int, data gin.H) {
	collections, err := handler.store.ListAll(c.Request.Context(), 0, 0)
	if err != nil {
		status = http.StatusInternalServerError
		if data["Error"] == nil || data["Error"] == "" {
			data["Error"] = "片单列表加载失败，请稍后重试"
		}
	}
	slots := make([]RelatedInput, 3)
	if editing, ok := data["Editing"].(*Collection); ok {
		copy(slots, editing.Related)
	}
	data["RelatedSlots"] = slots
	data["Collections"] = collections
	c.HTML(status, "admin_collections.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: "片单管理 - " + handler.config.SiteName, Robots: "noindex, nofollow",
	}, data))
}

// adminSave 新建或更新片单。
func (handler *Handler) adminSave(c *gin.Context) {
	id, _ := strconv.Atoi(c.PostForm("id"))
	title := strings.TrimSpace(c.PostForm("title"))
	saved := Collection{ID: id, Title: title, Slug: strings.TrimSpace(strings.ToLower(c.PostForm("slug"))),
		Description: strings.TrimSpace(c.PostForm("description")), Featured: c.PostForm("featured") == "on"}
	for slot := 0; slot < 3; slot++ {
		raw := c.PostForm(fmt.Sprintf("related_id_%d", slot))
		relatedID, err := strconv.Atoi(raw)
		if raw != "" && (err != nil || relatedID <= 0) {
			relatedID = -1
		}
		saved.Related = append(saved.Related, RelatedInput{ID: relatedID, Reason: strings.TrimSpace(c.PostForm(fmt.Sprintf("related_reason_%d", slot)))})
	}
	rawItems := c.PostForm("items")
	fail := func(status int, message string) {
		handler.renderAdmin(c, status, gin.H{"Error": message, "Editing": &saved, "EditingItems": rawItems})
	}
	if title == "" {
		fail(http.StatusUnprocessableEntity, "标题不能为空")
		return
	}
	slug := saved.Slug
	if slug == "" {
		slug = slugFromTitle(title)
	}
	if !ValidSlug(slug) {
		fail(http.StatusUnprocessableEntity, "地址不合法。中文标题生成不出地址，请手动填一个英文地址，例如 best-mystery-2024")
		return
	}
	items := ParseItems(rawItems)
	// 发布必须包含影片；草稿允许逐步补全。
	if saved.Featured && len(items) == 0 {
		fail(http.StatusUnprocessableEntity, "空片单不能发布，先加几部影片")
		return
	}
	saved.Slug = slug
	toSave := saved
	toSave.Related = nil
	for _, item := range saved.Related {
		if item.ID != 0 {
			toSave.Related = append(toSave.Related, item)
		}
	}
	_, unknown, err := handler.store.Save(c.Request.Context(), toSave, items)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.Is(err, ErrIncompletePublished) || errors.Is(err, ErrInvalidRelated) {
			fail(http.StatusUnprocessableEntity, err.Error())
		} else if errors.Is(err, ErrEmptyPublished) {
			fail(http.StatusUnprocessableEntity, "没有有效影片，不能发布空片单。请检查豆瓣 ID，或取消发布后保存草稿。")
		} else if errors.As(err, &databaseError) && databaseError.Code == "23505" && databaseError.ConstraintName == "collections_slug_key" {
			fail(http.StatusUnprocessableEntity, "这个片单地址已被使用，请换一个地址。填写的内容已保留。")
		} else {
			_ = c.Error(err)
			fail(http.StatusInternalServerError, "保存失败，请稍后重试。填写的内容已保留。")
		}
		return
	}
	notice := fmt.Sprintf("已保存《%s》，收录 %d 部", title, len(items)-len(unknown))
	handler.redirectToAdmin(c, notice, unknown)
}

// adminDelete 删除片单。
func (handler *Handler) adminDelete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.String(http.StatusBadRequest, "")
		return
	}
	if err := handler.store.Delete(c.Request.Context(), id); err != nil {
		c.String(http.StatusInternalServerError, "删除失败")
		return
	}
	c.Header("HX-Redirect", "/admin/collections?notice="+urlValue("片单已删除"))
	c.Status(http.StatusOK)
}

// redirectToAdmin 带着成功提示回到后台页。
func (handler *Handler) redirectToAdmin(c *gin.Context, notice string, unknown []string) {
	destination := "/admin/collections?notice=" + urlValue(notice)
	if len(unknown) > 0 {
		destination += "&unknown=" + urlValue(strings.Join(unknown, "、"))
	}
	c.Redirect(http.StatusFound, destination)
}

// notFound 渲染片单不存在时的 404。
func (handler *Handler) notFound(c *gin.Context) {
	c.HTML(http.StatusNotFound, "404.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: "片单未找到 - " + handler.config.SiteName, Robots: "noindex, follow",
	}, gin.H{"Path": c.Request.URL.Path}))
}

// isAdmin 判断当前请求是不是管理员，用于未发布片单的预览。
func isAdmin(c *gin.Context) bool {
	role, exists := c.Get("role")
	return exists && role == "admin"
}

// summarize 截断文本用于页面描述，按 rune 计数避免把中文截半。
func summarize(text string, limit int) string {
	text = strings.TrimSpace(text)
	characters := []rune(text)
	if len(characters) <= limit {
		return text
	}
	return string(characters[:limit]) + "…"
}

// urlValue 转义查询参数。
func urlValue(value string) string { return url.QueryEscape(value) }
