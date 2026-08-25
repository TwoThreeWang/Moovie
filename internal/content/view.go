// 本文件是 platform/web 渲染工具的本地别名，方便模板和测试直接使用。
package content

import (
	"html/template"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

// 转发 platform/web 的默认 SEO 文案，方便本包内直接使用。
const (
	DefaultDescription = platformweb.DefaultDescription
	DefaultKeywords    = platformweb.DefaultKeywords
)

// Metadata 和 ViewModel 是 platform/web 同名类型的别名。
type Metadata = platformweb.Metadata
type ViewModel = platformweb.ViewModel

// newViewModel 构造页面渲染数据。
func newViewModel(c *gin.Context, cfg config.Config, metadata Metadata) ViewModel {
	return platformweb.NewViewModel(c, cfg, metadata)
}

// canonicalURL 拼接规范链接（仅测试使用，实际渲染走 platform/web）。
func canonicalURL(siteURL, path string) string {
	return platformweb.CanonicalURL(siteURL, path)
}

// jsonLD 输出结构化数据并转义 </script>（仅测试使用）。
func jsonLD(value any) (template.JS, error) {
	return platformweb.JSONLD(value)
}
