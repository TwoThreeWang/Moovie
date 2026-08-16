package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

const maxResponseBytes = 8 << 20

type Case struct {
	Name                string   `json:"name"`
	Path                string   `json:"path"`
	Kind                string   `json:"kind"`
	CompareBody         bool     `json:"compare_body,omitempty"`
	RequiredEnv         []string `json:"required_env,omitempty"`
	ExpectedDifferences []string `json:"expected_differences,omitempty"`
	DifferenceReason    string   `json:"difference_reason,omitempty"`
}

type Manifest struct {
	Cases []Case `json:"cases"`
}

type Snapshot struct {
	Status             int
	ContentType        string
	Location           string
	Title              string
	Description        string
	Keywords           string
	Robots             string
	Canonical          string
	OGTitle            string
	OGDescription      string
	OGImage            string
	OGType             string
	TwitterCard        string
	TwitterTitle       string
	TwitterDescription string
	TwitterImage       string
	H1                 string
	IndexableText      string
	Links              []string
	StructuredData     []string
	Body               string
}

func LoadManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if len(manifest.Cases) == 0 {
		return Manifest{}, fmt.Errorf("manifest contains no cases")
	}
	for _, testCase := range manifest.Cases {
		for _, field := range testCase.ExpectedDifferences {
			if !knownDifferenceField(field) {
				return Manifest{}, fmt.Errorf("case %q has unsupported expected difference %q", testCase.Name, field)
			}
		}
		if len(testCase.ExpectedDifferences) > 0 && strings.TrimSpace(testCase.DifferenceReason) == "" {
			return Manifest{}, fmt.Errorf("case %q has expected differences but no difference_reason", testCase.Name)
		}
	}
	return manifest, nil
}

var differenceFields = map[string]struct{}{
	"status": {}, "content-type": {}, "location": {}, "title": {}, "description": {}, "keywords": {},
	"robots": {}, "canonical": {}, "og:title": {}, "og:description": {}, "og:image": {}, "og:type": {},
	"twitter:card": {}, "twitter:title": {}, "twitter:description": {}, "twitter:image": {}, "h1": {},
	"indexable-text": {}, "links": {}, "structured-data": {}, "body": {},
}

func knownDifferenceField(field string) bool {
	_, ok := differenceFields[strings.TrimSpace(field)]
	return ok
}

// FilterExpectedDifferences 只允许清单用例明确声明的字段差异，从而保持严格比较。
// 任何新出现的字段差异仍会被视为意外变化，并让兼容检查失败。
func FilterExpectedDifferences(differences, expected []string) (unexpected, explained []string) {
	allowed := make(map[string]struct{}, len(expected))
	for _, field := range expected {
		allowed[strings.TrimSpace(field)] = struct{}{}
	}
	for _, difference := range differences {
		field := difference
		if index := strings.Index(difference, " differs:"); index >= 0 {
			field = difference[:index]
		}
		if _, ok := allowed[field]; ok {
			explained = append(explained, difference)
			continue
		}
		unexpected = append(unexpected, difference)
	}
	return unexpected, explained
}

func Fetch(ctx context.Context, client *http.Client, baseURL string, testCase Case) (Snapshot, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse base URL: %w", err)
	}
	relative, err := url.Parse(testCase.Path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse case path: %w", err)
	}
	target := base.ResolveReference(relative)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	response, err := client.Do(request)
	if err != nil {
		return Snapshot{}, fmt.Errorf("request %s: %w", target, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read %s: %w", target, err)
	}
	if len(body) > maxResponseBytes {
		return Snapshot{}, fmt.Errorf("response %s exceeds %d bytes", target, maxResponseBytes)
	}

	snapshot := Snapshot{
		Status:      response.StatusCode,
		ContentType: mediaType(response.Header.Get("Content-Type")),
		Location:    response.Header.Get("Location"),
	}
	if testCase.CompareBody {
		snapshot.Body = normalizeText(string(body))
	}
	if testCase.Kind == "html" {
		seo, err := extractHTML(body)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse HTML from %s: %w", target, err)
		}
		seo.Status = snapshot.Status
		seo.ContentType = snapshot.ContentType
		seo.Location = snapshot.Location
		if testCase.CompareBody {
			seo.Body = snapshot.Body
		}
		return seo, nil
	}
	return snapshot, nil
}

func Compare(oldSnapshot, newSnapshot Snapshot) []string {
	checks := []struct {
		name string
		old  any
		new  any
	}{
		{"status", oldSnapshot.Status, newSnapshot.Status},
		{"content-type", oldSnapshot.ContentType, newSnapshot.ContentType},
		{"location", oldSnapshot.Location, newSnapshot.Location},
		{"title", oldSnapshot.Title, newSnapshot.Title},
		{"description", oldSnapshot.Description, newSnapshot.Description},
		{"keywords", oldSnapshot.Keywords, newSnapshot.Keywords},
		{"robots", oldSnapshot.Robots, newSnapshot.Robots},
		{"canonical", oldSnapshot.Canonical, newSnapshot.Canonical},
		{"og:title", oldSnapshot.OGTitle, newSnapshot.OGTitle},
		{"og:description", oldSnapshot.OGDescription, newSnapshot.OGDescription},
		{"og:image", oldSnapshot.OGImage, newSnapshot.OGImage},
		{"og:type", oldSnapshot.OGType, newSnapshot.OGType},
		{"twitter:card", oldSnapshot.TwitterCard, newSnapshot.TwitterCard},
		{"twitter:title", oldSnapshot.TwitterTitle, newSnapshot.TwitterTitle},
		{"twitter:description", oldSnapshot.TwitterDescription, newSnapshot.TwitterDescription},
		{"twitter:image", oldSnapshot.TwitterImage, newSnapshot.TwitterImage},
		{"h1", oldSnapshot.H1, newSnapshot.H1},
		{"indexable-text", oldSnapshot.IndexableText, newSnapshot.IndexableText},
		{"links", strings.Join(oldSnapshot.Links, "\n"), strings.Join(newSnapshot.Links, "\n")},
		{"structured-data", strings.Join(oldSnapshot.StructuredData, "\n"), strings.Join(newSnapshot.StructuredData, "\n")},
		{"body", oldSnapshot.Body, newSnapshot.Body},
	}

	differences := make([]string, 0)
	for _, check := range checks {
		if fmt.Sprint(check.old) != fmt.Sprint(check.new) {
			differences = append(differences, fmt.Sprintf("%s differs: old=%q new=%q", check.name, summarize(check.old), summarize(check.new)))
		}
	}
	return differences
}

func extractHTML(body []byte) (Snapshot, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return Snapshot{}, err
	}

	var snapshot Snapshot
	links := make(map[string]struct{})
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "title":
				if snapshot.Title == "" {
					snapshot.Title = nodeText(node)
				}
			case "h1":
				if snapshot.H1 == "" {
					snapshot.H1 = nodeText(node)
				}
			case "meta":
				name := strings.ToLower(attribute(node, "name"))
				property := strings.ToLower(attribute(node, "property"))
				content := strings.TrimSpace(attribute(node, "content"))
				switch name {
				case "description":
					snapshot.Description = content
				case "keywords":
					snapshot.Keywords = content
				case "robots":
					snapshot.Robots = content
				case "twitter:card":
					snapshot.TwitterCard = content
				case "twitter:title":
					snapshot.TwitterTitle = content
				case "twitter:description":
					snapshot.TwitterDescription = content
				case "twitter:image":
					snapshot.TwitterImage = content
				}
				switch property {
				case "og:title":
					snapshot.OGTitle = content
				case "og:description":
					snapshot.OGDescription = content
				case "og:image":
					snapshot.OGImage = content
				case "og:type":
					snapshot.OGType = content
				}
			case "link":
				if strings.EqualFold(attribute(node, "rel"), "canonical") {
					snapshot.Canonical = strings.TrimSpace(attribute(node, "href"))
				}
			case "script":
				if strings.EqualFold(attribute(node, "type"), "application/ld+json") {
					if normalized := normalizeJSON(nodeText(node)); normalized != "" {
						snapshot.StructuredData = append(snapshot.StructuredData, normalized)
					}
				}
			}
			if node.Data == "a" {
				if href := normalizedLink(attribute(node, "href")); href != "" {
					links[href] = struct{}{}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	snapshot.IndexableText = indexableBodyText(document)
	snapshot.Links = make([]string, 0, len(links))
	for link := range links {
		snapshot.Links = append(snapshot.Links, link)
	}
	sort.Strings(snapshot.Links)
	sort.Strings(snapshot.StructuredData)
	return snapshot, nil
}

func indexableBodyText(document *html.Node) string {
	var body *html.Node
	var find func(*html.Node)
	find = func(node *html.Node) {
		if body != nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "body" {
			body = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(document)
	if body == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, hidden bool) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "template", "svg":
				hidden = true
			}
		}
		if node.Type == html.TextNode && !hidden {
			builder.WriteByte(' ')
			builder.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(body, false)
	return normalizeText(builder.String())
}

func normalizedLink(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(strings.ToLower(value), "javascript:") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.Fragment = ""
	return parsed.String()
}

func summarize(value any) string {
	const maximum = 240
	text := fmt.Sprint(value)
	runes := []rune(text)
	if len(runes) <= maximum {
		return text
	}
	return string(runes[:maximum]) + fmt.Sprintf("… (%d chars)", len(runes))
}

func attribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func nodeText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return normalizeText(builder.String())
}

func normalizeJSON(raw string) string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return normalizeText(raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return normalizeText(raw)
	}
	return string(encoded)
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func mediaType(value string) string {
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.TrimSpace(strings.ToLower(value))
	}
	return strings.ToLower(parsed)
}
