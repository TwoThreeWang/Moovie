package search

import "testing"

// 资源站的豆瓣 ID 闸门必须与 worker 的 6~9 位判定一致，放宽一位都会造成永久失败重排。
func TestResourceDoubanIDMatchesWorkerRange(t *testing.T) {
	for _, ok := range []string{"123456", "1292052", "123456789"} {
		if !resourceDoubanID.MatchString(ok) {
			t.Fatalf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"123", "12345", "1234567890", "0123456", "12a456", ""} {
		if resourceDoubanID.MatchString(bad) {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}
