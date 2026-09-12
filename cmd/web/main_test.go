package main

import (
	"path/filepath"
	"testing"

	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
)

// 使用真实启动清单，防止删改模板后编译通过但启动失败。
func TestStartupTemplatesLoad(t *testing.T) {
	if _, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), contentPages); err != nil {
		t.Fatal(err)
	}
}
