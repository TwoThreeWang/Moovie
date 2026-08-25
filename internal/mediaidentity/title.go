package mediaidentity

import (
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/mediatitle"
)

// NormalizeTitle 生成规范别名使用的精确匹配键。展示文本始终单独保留，
// 规范化结果绝不会写回 media.title 或 media_aliases.alias。
func NormalizeTitle(value string) string {
	return mediatitle.Normalize(value)
}

// episodeNumberFromKey 从 S01E05 这样的键里取出集号，取不到返回 0。
func episodeNumberFromKey(key string) int {
	key = strings.ToUpper(strings.TrimSpace(key))
	index := strings.LastIndex(key, "E")
	if index < 0 || index == len(key)-1 {
		return 0
	}
	number := 0
	for _, current := range key[index+1:] {
		if current < '0' || current > '9' {
			return 0
		}
		number = number*10 + int(current-'0')
	}
	return number
}
