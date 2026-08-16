package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin/render"
)

const layoutName = "base.html"

var standalonePages = map[string]struct{}{
	"player_embed": {},
}

// Renderer 为每个页面保存一套独立解析的模板。每套只包含共享 layout 和一个页面，
// 防止不同页面中的同名模板定义相互泄漏。
type Renderer struct {
	templates map[string]compiledTemplate
}

type compiledTemplate struct {
	template *template.Template
	entry    string
}

func LoadRenderer(templatesDir string, pages []string) (Renderer, error) {
	loaded := make(map[string]compiledTemplate, len(pages))
	layoutPath := filepath.Join(templatesDir, "layouts", layoutName)
	partialPaths, err := filepath.Glob(filepath.Join(templatesDir, "partials", "*.html"))
	if err != nil {
		return Renderer{}, fmt.Errorf("find partial templates: %w", err)
	}

	for _, page := range pages {
		name := page + ".html"
		pagePath := filepath.Join(templatesDir, "pages", name)
		rootName := layoutName
		paths := append([]string{layoutPath, pagePath}, partialPaths...)
		if _, standalone := standalonePages[page]; standalone {
			rootName = name
			paths = []string{pagePath}
		}
		parsed, err := template.New(rootName).Funcs(templateFunctions()).ParseFiles(paths...)
		if err != nil {
			return Renderer{}, fmt.Errorf("parse template %s: %w", name, err)
		}
		loaded[name] = compiledTemplate{template: parsed, entry: rootName}
	}

	for _, partialPath := range partialPaths {
		filename := filepath.Base(partialPath)
		// 一个 partial 可能继续调用其他共享 partial（例如已看列表中的条目模板）。
		// 因此解析完整共享集合，同时保留外部调用使用的入口名称。
		parsed, err := template.New(filename).Funcs(templateFunctions()).ParseFiles(partialPaths...)
		if err != nil {
			return Renderer{}, fmt.Errorf("parse partial %s: %w", filename, err)
		}
		loaded["partials/"+filename] = compiledTemplate{template: parsed, entry: filename}
	}

	return Renderer{templates: loaded}, nil
}

func (r Renderer) Instance(name string, data any) render.Render {
	compiled := r.templates[name]
	return render.HTML{
		Template: compiled.template,
		Name:     compiled.entry,
		Data:     data,
	}
}

func templateFunctions() template.FuncMap {
	return template.FuncMap{
		"jsonUnmarshal": func(value string) []any {
			var decoded []any
			_ = json.Unmarshal([]byte(value), &decoded)
			return decoded
		},
		"proxyImg": func(value string) string {
			if value == "" || strings.HasPrefix(value, "/api/proxy/image") || strings.HasPrefix(value, "/static/") {
				return value
			}
			return "/api/proxy/image/r76RqSIVvUryzx" + base64.RawURLEncoding.EncodeToString([]byte(value))
		},
		"default": func(fallback, value any) any {
			switch typed := value.(type) {
			case string:
				if typed == "" {
					return fallback
				}
			case int:
				if typed == 0 {
					return fallback
				}
			case nil:
				return fallback
			}
			return value
		},
		"add":       func(a, b int) int { return a + b },
		"sub":       func(a, b int) int { return a - b },
		"daysSince": func(value time.Time) int { return int(time.Since(value).Hours() / 24) },
		"seq": func(start, end int) []int {
			values := make([]int, 0, end-start+1)
			for value := start; value <= end; value++ {
				values = append(values, value)
			}
			return values
		},
		"tof": func(value any) float64 {
			switch typed := value.(type) {
			case int:
				return float64(typed)
			case int64:
				return float64(typed)
			case float32:
				return float64(typed)
			case float64:
				return typed
			default:
				return 0
			}
		},
		"js": func(value string) template.JS {
			encoded, _ := json.Marshal(value)
			if len(encoded) < 2 {
				return ""
			}
			return template.JS(encoded[1 : len(encoded)-1]) // #nosec G203 -- JSON escaping makes quoted script content safe.
		},
		"divf": func(a, b int) float64 {
			if b == 0 {
				return 0
			}
			return float64(a) / float64(b)
		},
		"splitComma": func(value string) []string {
			if value == "" {
				return nil
			}
			parts := strings.Split(value, ",")
			result := make([]string, 0, len(parts))
			for _, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					result = append(result, trimmed)
				}
			}
			return result
		},
		"peopleNames": func(value string, limit int) string {
			var people []struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(value), &people) != nil {
				return value
			}
			names := make([]string, 0, limit)
			for _, p := range people {
				if len(names) >= limit {
					break
				}
				if p.Name != "" {
					names = append(names, p.Name)
				}
			}
			return strings.Join(names, " / ")
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires key/value pairs")
			}
			result := make(map[string]any, len(values)/2)
			for index := 0; index < len(values); index += 2 {
				key, ok := values[index].(string)
				if !ok {
					return nil, fmt.Errorf("dict key must be a string")
				}
				result[key] = values[index+1]
			}
			return result, nil
		},
	}
}
