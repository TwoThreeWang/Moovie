package social

import (
	"path/filepath"
	"testing"

	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
)

// TestCommunityTemplatesCompile 守住社区几张页面和共用 partial 能被解析。
// follow_button.html 同时被片场、公开主页、短评页和关注动态调用，
// 任何一处改坏了名字或参数，这里就会先炸，不用等启动进程。
func TestCommunityTemplatesCompile(t *testing.T) {
	templatesDir := filepath.Join("..", "..", "web", "templates")
	if _, err := platformweb.LoadRenderer(templatesDir, []string{"feed", "following", "review", "cinema", "share", "404"}); err != nil {
		t.Fatalf("社区模板解析失败: %v", err)
	}
}
