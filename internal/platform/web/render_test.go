package web

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRendererKeepsPageDefinitionsIsolated(t *testing.T) {
	templatesDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(templatesDir, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(templatesDir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(templatesDir, "layouts", layoutName), `{{template "content" .}}`)
	writeTestFile(t, filepath.Join(templatesDir, "pages", "first.html"), `{{define "content"}}first: {{.Title}}{{end}}`)
	writeTestFile(t, filepath.Join(templatesDir, "pages", "second.html"), `{{define "content"}}second: {{.Title}}{{end}}`)

	renderer, err := LoadRenderer(templatesDir, []string{"first", "second"})
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}

	var output bytes.Buffer
	if err := renderer.templates["first.html"].template.ExecuteTemplate(&output, layoutName, map[string]string{"Title": "page"}); err != nil {
		t.Fatalf("render first template: %v", err)
	}
	if got := strings.TrimSpace(output.String()); got != "first: page" {
		t.Fatalf("rendered output = %q, want %q", got, "first: page")
	}
}

func TestJSTemplateFunctionEscapesScriptTerminator(t *testing.T) {
	jsFunction := templateFunctions()["js"].(func(string) template.JS)
	encoded := string(jsFunction(`</script><script>alert("x")</script>`))
	if strings.Contains(encoded, "</script>") || !strings.Contains(encoded, `\u003c`) {
		t.Fatalf("unsafe JavaScript string content: %s", encoded)
	}
}

func TestProxyImageTemplateFunctionPreservesLocalFallback(t *testing.T) {
	proxyImage := templateFunctions()["proxyImg"].(func(string) string)
	for _, path := range []string{"/static/img/placeholder.svg", "/api/proxy/image/existing"} {
		if got := proxyImage(path); got != path {
			t.Fatalf("proxyImg(%q) = %q", path, got)
		}
	}
	if got := proxyImage("https://image.example/poster.jpg"); !strings.HasPrefix(got, "/api/proxy/image/") {
		t.Fatalf("external image was not proxied: %q", got)
	}
}

func TestLoadRendererSupportsStandalonePlayerEmbed(t *testing.T) {
	templatesDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(templatesDir, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(templatesDir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(templatesDir, "layouts", layoutName), `layout`)
	writeTestFile(t, filepath.Join(templatesDir, "pages", "player_embed.html"), `standalone: {{.URL}}`)

	renderer, err := LoadRenderer(templatesDir, []string{"player_embed"})
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}
	compiled := renderer.templates["player_embed.html"]
	var output bytes.Buffer
	if err := compiled.template.ExecuteTemplate(&output, compiled.entry, map[string]string{"URL": "https://video.example/test.m3u8"}); err != nil {
		t.Fatalf("render standalone template: %v", err)
	}
	if got := strings.TrimSpace(output.String()); got != "standalone: https://video.example/test.m3u8" {
		t.Fatalf("rendered output = %q", got)
	}
}

func TestLoadRendererMakesSharedPartialsAvailableToPages(t *testing.T) {
	templatesDir := t.TempDir()
	for _, directory := range []string{"layouts", "pages", "partials"} {
		if err := os.Mkdir(filepath.Join(templatesDir, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(templatesDir, "layouts", layoutName), `{{template "content" .}}`)
	writeTestFile(t, filepath.Join(templatesDir, "pages", "play.html"), `{{define "content"}}{{template "shared.html" (dict "Title" .Title)}}{{end}}`)
	writeTestFile(t, filepath.Join(templatesDir, "partials", "shared.html"), `{{define "shared.html"}}shared: {{.Title}}{{end}}`)

	renderer, err := LoadRenderer(templatesDir, []string{"play"})
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}
	compiled := renderer.templates["play.html"]
	var output bytes.Buffer
	if err := compiled.template.ExecuteTemplate(&output, compiled.entry, map[string]string{"Title": "movie"}); err != nil {
		t.Fatalf("render page with partial: %v", err)
	}
	if got := strings.TrimSpace(output.String()); got != "shared: movie" {
		t.Fatalf("rendered output = %q", got)
	}
}

func TestNotificationsPageRendersItsStylesAndOnlyOneActiveSidebarItem(t *testing.T) {
	renderer, err := LoadRenderer(filepath.Join("..", "..", "..", "web", "templates"), []string{"notifications"})
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}
	compiled := renderer.templates["notifications.html"]
	data := map[string]any{
		"Title": "消息", "ActiveMenu": "notifications", "Notifications": []any{},
		"UserInfo": struct {
			ID       int
			Username string
			Role     string
		}{ID: 1, Username: "tester", Role: "user"},
	}
	var output bytes.Buffer
	if err := compiled.template.ExecuteTemplate(&output, compiled.entry, data); err != nil {
		t.Fatalf("render notifications page: %v", err)
	}
	html := output.String()
	if !strings.Contains(html, ".notifications-page {") {
		t.Fatal("notifications page styles were not rendered")
	}
	if strings.Count(html, `class="nav-item active"`) != 1 {
		t.Fatalf("active sidebar items = %d, want 1", strings.Count(html, `class="nav-item active"`))
	}
}

func TestSettingsTogglesDoNotBypassCSRFSubmitHandler(t *testing.T) {
	for _, name := range []string{"dashboard.html", "settings.html"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "templates", "pages", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(contents), ".form."+"submit()") {
			t.Fatalf("%s bypasses the submit event and CSRF token injection", name)
		}
	}
}

func TestStandalonePartialCanRenderAnotherPartial(t *testing.T) {
	templatesDir := t.TempDir()
	for _, directory := range []string{"layouts", "pages", "partials"} {
		if err := os.Mkdir(filepath.Join(templatesDir, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(templatesDir, "layouts", layoutName), `layout`)
	writeTestFile(t, filepath.Join(templatesDir, "partials", "list.html"), `{{range .}}{{template "item.html" .}}{{end}}`)
	writeTestFile(t, filepath.Join(templatesDir, "partials", "item.html"), `{{define "item.html"}}[{{.}}]{{end}}`)

	renderer, err := LoadRenderer(templatesDir, nil)
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}
	compiled := renderer.templates["partials/list.html"]
	var output bytes.Buffer
	if err := compiled.template.ExecuteTemplate(&output, compiled.entry, []string{"first", "second"}); err != nil {
		t.Fatalf("render nested partial: %v", err)
	}
	if got := strings.TrimSpace(output.String()); got != "[first][second]" {
		t.Fatalf("rendered output = %q", got)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
