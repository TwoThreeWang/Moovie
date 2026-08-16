package compat

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxSitemapBytes = 32 << 20

type SitemapEntry struct {
	Location   string `xml:"loc"`
	LastMod    string `xml:"lastmod"`
	ChangeFreq string `xml:"changefreq"`
	Priority   string `xml:"priority"`
}

func FetchSitemap(ctx context.Context, client *http.Client, baseURL string) (map[string]struct{}, error) {
	entries, err := FetchSitemapEntries(ctx, client, baseURL)
	if err != nil {
		return nil, err
	}
	return entryURLSet(entries), nil
}

func FetchSitemapEntries(ctx context.Context, client *http.Client, baseURL string) (map[string]SitemapEntry, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse sitemap base URL: %w", err)
	}
	target := base.ResolveReference(&url.URL{Path: "/sitemap.xml"})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request sitemap: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sitemap returned HTTP %d", response.StatusCode)
	}
	return ExtractSitemapEntries(io.LimitReader(response.Body, maxSitemapBytes+1))
}

func ExtractSitemapURLs(reader io.Reader) (map[string]struct{}, error) {
	entries, err := ExtractSitemapEntries(reader)
	if err != nil {
		return nil, err
	}
	return entryURLSet(entries), nil
}

func ExtractSitemapEntries(reader io.Reader) (map[string]SitemapEntry, error) {
	decoder := xml.NewDecoder(reader)
	entries := make(map[string]SitemapEntry)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode sitemap XML: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "url" {
			continue
		}
		var entry SitemapEntry
		if err := decoder.DecodeElement(&entry, &start); err != nil {
			return nil, fmt.Errorf("decode sitemap URL: %w", err)
		}
		entry.Location = strings.TrimSpace(entry.Location)
		entry.LastMod = strings.TrimSpace(entry.LastMod)
		entry.ChangeFreq = strings.TrimSpace(entry.ChangeFreq)
		entry.Priority = strings.TrimSpace(entry.Priority)
		location := entry.Location
		parsed, err := url.Parse(location)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid sitemap URL %q", location)
		}
		if entry.LastMod != "" && !validLastMod(entry.LastMod) {
			return nil, fmt.Errorf("invalid sitemap lastmod %q for %q", entry.LastMod, location)
		}
		if _, duplicate := entries[location]; duplicate {
			return nil, fmt.Errorf("duplicate sitemap URL %q", location)
		}
		entries[location] = entry
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("sitemap contains no URLs")
	}
	return entries, nil
}

func CompareSitemapURLs(oldURLs, newURLs map[string]struct{}) (missing, extra []string) {
	for location := range oldURLs {
		if _, exists := newURLs[location]; !exists {
			missing = append(missing, location)
		}
	}
	for location := range newURLs {
		if _, exists := oldURLs[location]; !exists {
			extra = append(extra, location)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func CompareSitemapMetadata(oldEntries, newEntries map[string]SitemapEntry) []string {
	differences := make([]string, 0)
	for location, oldEntry := range oldEntries {
		newEntry, exists := newEntries[location]
		if !exists {
			continue
		}
		if oldEntry.ChangeFreq != newEntry.ChangeFreq {
			differences = append(differences, fmt.Sprintf("%s changefreq old=%q new=%q", location, oldEntry.ChangeFreq, newEntry.ChangeFreq))
		}
		if oldEntry.Priority != newEntry.Priority {
			differences = append(differences, fmt.Sprintf("%s priority old=%q new=%q", location, oldEntry.Priority, newEntry.Priority))
		}
	}
	sort.Strings(differences)
	return differences
}

func entryURLSet(entries map[string]SitemapEntry) map[string]struct{} {
	urls := make(map[string]struct{}, len(entries))
	for location := range entries {
		urls[location] = struct{}{}
	}
	return urls
}

func validLastMod(value string) bool {
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
