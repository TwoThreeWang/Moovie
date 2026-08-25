package content

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// sitemapMovieLimit 限制进 sitemap 的影片数量。
const sitemapMovieLimit = 1000

// SitemapMovie 是 sitemap 需要的影片信息。
type SitemapMovie struct {
	DoubanID  string
	UpdatedAt time.Time
}

// SitemapMovieProvider 由 catalog 实现，提供最近更新的影片。
type SitemapMovieProvider interface {
	LatestForSitemap(ctx context.Context, limit int) ([]SitemapMovie, error)
}

// sitemapURL 是 sitemap 中的一条 URL。
type sitemapURL struct {
	Location   string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq"`
	Priority   string `xml:"priority"`
}

// sitemapDocument 是 sitemap 的 XML 根节点。
type sitemapDocument struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

// staticSitemapPages 是固定收录的页面及其权重。
var staticSitemapPages = []struct {
	path      string
	priority  string
	frequency string
}{
	{path: "/", priority: "1.0", frequency: "daily"},
	{path: "/discover/movie", priority: "0.8", frequency: "daily"},
	{path: "/discover/tv", priority: "0.8", frequency: "daily"},
	{path: "/discover/show", priority: "0.8", frequency: "daily"},
	{path: "/discover/cartoon", priority: "0.8", frequency: "daily"},
	{path: "/trends", priority: "0.8", frequency: "daily"},
	{path: "/feedback", priority: "0.5", frequency: "monthly"},
	{path: "/changelog", priority: "0.5", frequency: "weekly"},
	{path: "/about", priority: "0.5", frequency: "monthly"},
	{path: "/dmca", priority: "0.5", frequency: "monthly"},
	{path: "/privacy", priority: "0.5", frequency: "monthly"},
	{path: "/terms", priority: "0.5", frequency: "monthly"},
}

// buildSitemap 生成 sitemap：固定页面 + 最近更新的影片详情页和相似推荐页。
// 取影片失败时只返回固定页面，不让整个 sitemap 挂掉。
func buildSitemap(ctx context.Context, siteURL string, provider SitemapMovieProvider) ([]byte, error) {
	baseURL := strings.TrimRight(siteURL, "/")
	document := sitemapDocument{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  make([]sitemapURL, 0, len(staticSitemapPages)),
	}
	for _, page := range staticSitemapPages {
		document.URLs = append(document.URLs, sitemapURL{
			Location:   baseURL + page.path,
			ChangeFreq: page.frequency,
			Priority:   page.priority,
		})
	}

	if provider != nil {
		movies, err := provider.LatestForSitemap(ctx, sitemapMovieLimit)
		if err == nil {
			for _, movie := range movies {
				lastModified := movie.UpdatedAt.Format("2006-01-02")
				document.URLs = append(document.URLs,
					sitemapURL{Location: fmt.Sprintf("%s/movie/%s", baseURL, movie.DoubanID), LastMod: lastModified, ChangeFreq: "weekly", Priority: "0.7"},
					sitemapURL{Location: fmt.Sprintf("%s/similar/%s", baseURL, movie.DoubanID), LastMod: lastModified, ChangeFreq: "weekly", Priority: "0.6"},
				)
			}
		}
	}

	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal sitemap: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}
