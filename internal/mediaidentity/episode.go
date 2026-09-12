package mediaidentity

import "github.com/TwoThreeWang/Moovie/new/internal/playurl"

// NormalizeEpisodeLabel 与采集和实时解析共用内容身份规则。
func NormalizeEpisodeLabel(label string) (int, string) { return playurl.NormalizeEpisodeLabel(label) }
