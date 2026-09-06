package collection

import (
	"path/filepath"

	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
)

// compileCollectionTemplates 解析片单相关页面，供测试调用。
func compileCollectionTemplates() error {
	_, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"),
		[]string{"collections", "collection", "admin_collections", "404"})
	return err
}
