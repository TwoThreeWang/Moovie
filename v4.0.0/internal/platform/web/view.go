package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

// 页面没有单独设置时使用的默认 SEO 描述和关键词。
const (
	DefaultDescription = "Moovie 影牛是一个全网影视聚合搜索网站，支持一键搜索各大资源平台的电影、电视剧和综艺节目，让你轻松找到想看的影片。"
	DefaultKeywords    = "Moovie, 影牛, 影视搜索, 电影搜索, 电视剧搜索, 在线看电影, 聚合搜索, 全网视频, 豆瓣热门"
)

// Metadata 是页面的 SEO 元信息：标题、描述、canonical 和结构化数据。
type Metadata struct {
	Title       string
	Description string
	Keywords    string
	Robots      string
	Canonical   string
	Cover       string
	JSONLD      []template.JS
}

// ViewModel 是所有页面共享的基础渲染数据（站点信息、当前路径、登录用户、导航高亮）。
type ViewModel struct {
	Metadata
	SiteName     string
	SiteUrl      string
	Path         string
	FullPath     string
	Referer      string
	ActiveMenu   string
	ContentClass string
	MovieTitle   string
	UserInfo     any
	Keyword      string
	Bypass       bool
}

// NewViewModel 从当前请求构造基础视图模型，Robots 默认允许收录。
func NewViewModel(c *gin.Context, cfg config.Config, metadata Metadata) ViewModel {
	if metadata.Robots == "" {
		metadata.Robots = "index, follow"
	}
	view := ViewModel{
		Metadata:   metadata,
		SiteName:   cfg.SiteName,
		SiteUrl:    cfg.SiteURL,
		Path:       c.Request.URL.Path,
		FullPath:   c.Request.RequestURI,
		Referer:    c.Request.Referer(),
		ActiveMenu: ActiveMenu(c.Request.URL.Path, c.Query("type")),
	}
	if user, exists := c.Get("user_info"); exists {
		view.UserInfo = user
	}
	return view
}

// NewData 把基础视图模型摊平成 gin.H，再合并页面自己的数据。模板里直接用 .Title 这类字段。
func NewData(c *gin.Context, cfg config.Config, metadata Metadata, extra gin.H) gin.H {
	view := NewViewModel(c, cfg, metadata)
	data := gin.H{
		"Title": view.Title, "Description": view.Description, "Keywords": view.Keywords,
		"Robots": view.Robots, "Canonical": view.Canonical, "Cover": view.Cover, "JSONLD": view.JSONLD,
		"SiteName": view.SiteName, "SiteUrl": view.SiteUrl, "Path": view.Path,
		"FullPath": view.FullPath, "Referer": view.Referer, "ActiveMenu": view.ActiveMenu,
		"UserInfo": view.UserInfo,
	}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

// CanonicalURL 拼出页面的规范链接，避免同一内容出现多个可收录地址。
func CanonicalURL(siteURL, path string) string {
	return strings.TrimRight(siteURL, "/") + "/" + strings.TrimLeft(path, "/")
}

// JSONLD 把结构化数据序列化成可直接嵌入 <script> 的内容。
func JSONLD(value any) (template.JS, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode JSON-LD: %w", err)
	}
	return template.JS(encoded), nil // #nosec G203 -- encoding/json escapes HTML-significant characters.
}

// ActiveMenu 根据路径决定导航栏高亮哪一项。
func ActiveMenu(path, searchType string) string {
	if strings.HasPrefix(path, "/dashboard") || path == "/history" || path == "/settings" {
		return "user"
	}
	if strings.HasPrefix(path, "/admin") {
		return "admin"
	}
	if path == "/search" {
		if searchType != "" {
			return searchType
		}
		return "search"
	}
	switch path {
	case "/":
		return "home"
	case "/discover":
		return "discover"
	case "/trends":
		return "trends"
	case "/foryou":
		return "foryou"
	case "/cinema":
		return "cinema"
	case "/player":
		return "player"
	case "/iptv":
		return "iptv"
	case "/feedback":
		return "feedback"
	case "/about":
		return "about"
	case "/advertise":
		return "advertise"
	default:
		return ""
	}
}
