package datamigrate

import "testing"

// 豆瓣 ID 必须是正整数；旧历史中的空值和占位值 0 不能生成伪媒体记录。
func TestNormalizeMigratedDoubanIDRejectsPlaceholders(t *testing.T) {
	tests := map[string]string{
		"1292052":   "1292052",
		" 1292052 ": "1292052",
		"":          "",
		"0":         "",
		"-1":        "",
		"unknown":   "",
	}
	for input, expected := range tests {
		if actual := normalizeMigratedDoubanID(input); actual != expected {
			t.Fatalf("normalizeMigratedDoubanID(%q) = %q，期望 %q", input, actual, expected)
		}
	}
}
