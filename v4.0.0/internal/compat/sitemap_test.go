package compat

import (
	"strings"
	"testing"
)

func TestExtractAndCompareSitemapURLSetsIgnoresLastmodAndOrder(t *testing.T) {
	oldXML := `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://example.com/</loc><lastmod>2026-01-01</lastmod></url>
<url><loc>https://example.com/movie/1</loc></url></urlset>`
	newXML := `<?xml version="1.0"?><urlset><url><loc>https://example.com/movie/2</loc></url>
<url><loc>https://example.com/</loc><lastmod>2026-07-30</lastmod></url></urlset>`
	oldURLs, err := ExtractSitemapURLs(strings.NewReader(oldXML))
	if err != nil {
		t.Fatal(err)
	}
	newURLs, err := ExtractSitemapURLs(strings.NewReader(newXML))
	if err != nil {
		t.Fatal(err)
	}
	missing, extra := CompareSitemapURLs(oldURLs, newURLs)
	if len(missing) != 1 || missing[0] != "https://example.com/movie/1" || len(extra) != 1 || extra[0] != "https://example.com/movie/2" {
		t.Fatalf("missing/extra = %v/%v", missing, extra)
	}
}

func TestExtractSitemapRejectsDuplicateAndRelativeURLs(t *testing.T) {
	for _, input := range []string{
		`<urlset><url><loc>/relative</loc></url></urlset>`,
		`<urlset><url><loc>https://example.com/a</loc></url><url><loc>https://example.com/a</loc></url></urlset>`,
		`<urlset><url><loc>https://example.com/a</loc><lastmod>yesterday</lastmod></url></urlset>`,
	} {
		if _, err := ExtractSitemapURLs(strings.NewReader(input)); err == nil {
			t.Fatalf("ExtractSitemapURLs(%q) error = nil", input)
		}
	}
}

func TestCompareSitemapMetadataPreservesFrequencyAndPriorityButAllowsLastmodUpdates(t *testing.T) {
	oldEntries, err := ExtractSitemapEntries(strings.NewReader(`<urlset><url><loc>https://example.com/a</loc><lastmod>2026-01-01</lastmod><changefreq>weekly</changefreq><priority>0.7</priority></url></urlset>`))
	if err != nil {
		t.Fatal(err)
	}
	newEntries, err := ExtractSitemapEntries(strings.NewReader(`<urlset><url><loc>https://example.com/a</loc><lastmod>2026-07-30T00:00:00Z</lastmod><changefreq>daily</changefreq><priority>0.8</priority></url></urlset>`))
	if err != nil {
		t.Fatal(err)
	}
	differences := CompareSitemapMetadata(oldEntries, newEntries)
	if len(differences) != 2 || !strings.Contains(strings.Join(differences, "\n"), "changefreq") || !strings.Contains(strings.Join(differences, "\n"), "priority") {
		t.Fatalf("differences = %v", differences)
	}
}
