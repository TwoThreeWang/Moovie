package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

const (
	DefaultDescription = "Moovie 影牛是一个全网影视聚合搜索网站，支持一键搜索各大资源平台的电影、电视剧和综艺节目，让你轻松找到想看的影片。"
	DefaultKeywords    = "Moovie, 影牛, 影视搜索, 电影搜索, 电视剧搜索, 在线看电影, 聚合搜索, 全网视频, 豆瓣热门"
)

type Metadata struct {
	Title       string
	Description string
	Keywords    string
	Robots      string
	Canonical   string
	Cover       string
	JSONLD      []template.JS
}

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

func CanonicalURL(siteURL, path string) string {
	return strings.TrimRight(siteURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func JSONLD(value any) (template.JS, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode JSON-LD: %w", err)
	}
	return template.JS(encoded), nil // #nosec G203 -- encoding/json escapes HTML-significant characters.
}

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
