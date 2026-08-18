package compat

import (
	"strings"
	"testing"
)

func TestExtractHTML(t *testing.T) {
	html := `<!doctype html><html><head>
<title> Movie title </title>
<meta content="A description" name="description">
<meta property="og:title" content="OG title">
<link href="https://example.com/movie/1" rel="canonical">
<script type="application/ld+json">{"name":"Movie","@context":"https://schema.org"}</script>
</head><body><h1> Movie heading </h1><p>Visible <strong>copy</strong></p>
<a href="/movie/1#reviews">Movie</a><a href="https://external.example/path#top">External</a>
<script>ignored script words</script><style>.ignored { display: none }</style></body></html>`

	snapshot, err := extractHTML([]byte(html))
	if err != nil {
		t.Fatalf("extractHTML() error = %v", err)
	}
	if snapshot.Title != "Movie title" {
		t.Fatalf("Title = %q", snapshot.Title)
	}
	if snapshot.Description != "A description" {
		t.Fatalf("Description = %q", snapshot.Description)
	}
	if snapshot.Canonical != "https://example.com/movie/1" {
		t.Fatalf("Canonical = %q", snapshot.Canonical)
	}
	if snapshot.H1 != "Movie heading" {
		t.Fatalf("H1 = %q", snapshot.H1)
	}
	if snapshot.IndexableText != "Movie heading Visible copy Movie External" {
		t.Fatalf("IndexableText = %q", snapshot.IndexableText)
	}
	wantLinks := []string{"/movie/1", "https://external.example/path"}
	if len(snapshot.Links) != len(wantLinks) || snapshot.Links[0] != wantLinks[0] || snapshot.Links[1] != wantLinks[1] {
		t.Fatalf("Links = %#v", snapshot.Links)
	}
	if len(snapshot.StructuredData) != 1 || !strings.Contains(snapshot.StructuredData[0], `"name":"Movie"`) {
		t.Fatalf("StructuredData = %#v", snapshot.StructuredData)
	}
}

func TestNormalizedLinkDropsFragmentsAndUnsafeEmptyValues(t *testing.T) {
	for input, expected := range map[string]string{
		"/movie/1#reviews":            "/movie/1",
		"https://example.com/a?q=1#x": "https://example.com/a?q=1",
		"#content":                    "",
		"javascript:alert(1)":         "",
	} {
		if got := normalizedLink(input); got != expected {
			t.Fatalf("normalizedLink(%q) = %q, want %q", input, got, expected)
		}
	}
}
