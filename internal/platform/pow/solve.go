package pow

import (
	"strconv"
	"strings"
)

// Solve 从 0 开始递增，返回 token 的第一个有效 nonce；token 格式不对时返回空串。
// 线上由浏览器的 pow.js 求解，这里只供其他包的测试和本地验证脚本使用。
func Solve(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 5 {
		return ""
	}
	bits, err := strconv.Atoi(parts[2])
	if err != nil || bits < MinBits || bits > MaxBits {
		return ""
	}
	for nonce := 0; ; nonce++ {
		candidate := strconv.Itoa(nonce)
		if Satisfies(parts[3], candidate, bits) {
			return candidate
		}
	}
}
