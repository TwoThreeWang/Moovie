package content

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const sitemapPageSize = 5000

type SitemapKind string

const (
	SitemapMovies  SitemapKind = "movies"
	SitemapSimilar SitemapKind = "similar"
)

type SitemapMovie struct {
	DoubanID  string
	UpdatedAt time.Time
}

type SitemapMovieProvider interface {
	CountForSitemap(ctx context.Context, kind SitemapKind) (int, error)
	PageForSitemap(ctx context.Context, kind SitemapKind, limit, offset int) ([]SitemapMovie, error)
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

type sitemapIndexEntry struct {
	Location string `xml:"loc"`
}

type sitemapIndexDocument struct {
	XMLName xml.Name            `xml:"sitemapindex"`
	XMLNS   string              `xml:"xmlns,attr"`
	Entries []sitemapIndexEntry `xml:"sitemap"`
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
	{path: "/cinema", priority: "0.7", frequency: "daily"},
	{path: "/player", priority: "0.6", frequency: "weekly"},
	{path: "/iptv", priority: "0.6", frequency: "weekly"},
	{path: "/tvbox", priority: "0.6", frequency: "weekly"},
	{path: "/feedback", priority: "0.5", frequency: "monthly"},
	{path: "/changelog", priority: "0.5", frequency: "weekly"},
	{path: "/about", priority: "0.5", frequency: "monthly"},
	{path: "/advertise", priority: "0.5", frequency: "monthly"},
	{path: "/dmca", priority: "0.5", frequency: "monthly"},
	{path: "/privacy", priority: "0.5", frequency: "monthly"},
	{path: "/terms", priority: "0.5", frequency: "monthly"},
}

func buildSitemapIndex(ctx context.Context, siteURL string, provider SitemapMovieProvider) ([]byte, error) {
	baseURL := strings.TrimRight(siteURL, "/")
	document := sitemapIndexDocument{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", Entries: []sitemapIndexEntry{{Location: baseURL + "/sitemaps/static.xml"}}}
	if provider != nil {
		for _, kind := range []SitemapKind{SitemapMovies, SitemapSimilar} {
			count, err := provider.CountForSitemap(ctx, kind)
			if err != nil {
				return nil, fmt.Errorf("count %s sitemap: %w", kind, err)
			}
			for page := 1; page <= sitemapPageCount(count); page++ {
				document.Entries = append(document.Entries, sitemapIndexEntry{Location: fmt.Sprintf("%s/sitemaps/%s/%d.xml", baseURL, kind, page)})
			}
		}
	}
	return marshalSitemap(document)
}

func buildStaticSitemap(siteURL string) ([]byte, error) {
	baseURL := strings.TrimRight(siteURL, "/")
	document := sitemapDocument{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: make([]sitemapURL, 0, len(staticSitemapPages))}
	for _, page := range staticSitemapPages {
		document.URLs = append(document.URLs, sitemapURL{Location: baseURL + page.path, ChangeFreq: page.frequency, Priority: page.priority})
	}
	return marshalSitemap(document)
}

func buildMediaSitemap(ctx context.Context, siteURL string, provider SitemapMovieProvider, kind SitemapKind, page int) ([]byte, bool, error) {
	if provider == nil || page < 1 {
		return nil, false, nil
	}
	count, err := provider.CountForSitemap(ctx, kind)
	if err != nil {
		return nil, false, fmt.Errorf("count %s sitemap: %w", kind, err)
	}
	if page > sitemapPageCount(count) {
		return nil, false, nil
	}
	movies, err := provider.PageForSitemap(ctx, kind, sitemapPageSize, (page-1)*sitemapPageSize)
	if err != nil {
		return nil, false, fmt.Errorf("load %s sitemap page %d: %w", kind, page, err)
	}
	baseURL := strings.TrimRight(siteURL, "/")
	document := sitemapDocument{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: make([]sitemapURL, 0, len(movies))}
	for _, movie := range movies {
		path, priority := "/movie/", "0.7"
		if kind == SitemapSimilar {
			path, priority = "/similar/", "0.6"
		}
		document.URLs = append(document.URLs, sitemapURL{Location: baseURL + path + movie.DoubanID, LastMod: movie.UpdatedAt.Format("2006-01-02"), ChangeFreq: "weekly", Priority: priority})
	}
	body, err := marshalSitemap(document)
	return body, true, err
}

func sitemapPageCount(count int) int { return (count + sitemapPageSize - 1) / sitemapPageSize }

func marshalSitemap(document any) ([]byte, error) {
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal sitemap: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}
