package content

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const sitemapMovieLimit = 1000

type SitemapMovie struct {
	DoubanID  string
	UpdatedAt time.Time
}

type SitemapMovieProvider interface {
	LatestForSitemap(ctx context.Context, limit int) ([]SitemapMovie, error)
}

type sitemapURL struct {
	Location   string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq"`
	Priority   string `xml:"priority"`
}

type sitemapDocument struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

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
