package mediatitle

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize 生成媒体匹配和统一搜索共用的规范别名键，展示标题始终单独保留。
func Normalize(value string) string {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	var normalized strings.Builder
	normalized.Grow(len(value))
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsNumber(current) || unicode.IsMark(current) {
			normalized.WriteRune(current)
		}
	}
	return normalized.String()
}
