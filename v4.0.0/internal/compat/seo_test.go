package compat

import (
	"os"
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

func TestCompareReportsSEOChanges(t *testing.T) {
	oldSnapshot := Snapshot{Status: 200, Title: "Old", Canonical: "https://example.com/a", IndexableText: "old copy", Links: []string{"/a"}}
	newSnapshot := Snapshot{Status: 200, Title: "New", Canonical: "https://example.com/b", IndexableText: "new copy", Links: []string{"/b"}}
	differences := Compare(oldSnapshot, newSnapshot)
	if len(differences) != 4 {
		t.Fatalf("differences = %#v, want 4", differences)
	}
}

func TestFilterExpectedDifferencesKeepsUnexpectedFields(t *testing.T) {
	differences := []string{
		`title differs: old="old" new="new"`,
		`indexable-text differs: old="old copy" new="new copy"`,
	}
	unexpected, explained := FilterExpectedDifferences(differences, []string{"indexable-text"})
	if len(unexpected) != 1 || unexpected[0] != differences[0] {
		t.Fatalf("unexpected = %#v", unexpected)
	}
	if len(explained) != 1 || explained[0] != differences[1] {
		t.Fatalf("explained = %#v", explained)
	}
}

func TestLoadManifestRejectsUnsupportedExpectedDifference(t *testing.T) {
	_, err := LoadManifest(strings.NewReader(`{"cases":[{"name":"home","path":"/","kind":"html","expected_differences":["title"],"difference_reason":"test"}]}`))
	if err != nil {
		t.Fatalf("supported expected difference rejected: %v", err)
	}
	_, err = LoadManifest(strings.NewReader(`{"cases":[{"name":"home","path":"/","kind":"html","expected_differences":["not-a-field"],"difference_reason":"test"}]}`))
	if err == nil {
		t.Fatal("unsupported expected difference was accepted")
	}
	_, err = LoadManifest(strings.NewReader(`{"cases":[{"name":"home","path":"/","kind":"html","expected_differences":["title"]}]}`))
	if err == nil {
		t.Fatal("missing expected difference reason was accepted")
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

func TestLoadManifestRejectsUnknownFields(t *testing.T) {
	_, err := LoadManifest(strings.NewReader(`{"cases":[{"name":"home","path":"/","kind":"html","unknown":true}]}`))
	if err == nil {
		t.Fatal("LoadManifest() error = nil, want unknown field error")
	}
}

func TestSEOManifestIsValid(t *testing.T) {
	file, err := os.Open("../../compat/seo_cases.json")
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer file.Close()

	manifest, err := LoadManifest(file)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	seen := make(map[string]struct{}, len(manifest.Cases))
	for _, testCase := range manifest.Cases {
		if testCase.Name == "" || !strings.HasPrefix(testCase.Path, "/") {
			t.Errorf("invalid case: %#v", testCase)
		}
		if testCase.Kind != "html" && testCase.Kind != "text" && testCase.Kind != "http" {
			t.Errorf("case %q has unsupported kind %q", testCase.Name, testCase.Kind)
		}
		if _, exists := seen[testCase.Name]; exists {
			t.Errorf("duplicate case name %q", testCase.Name)
		}
		seen[testCase.Name] = struct{}{}
	}
}
