package catalog

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

// maxRetryAfter 给上游的 Retry-After 建议封顶。免费接口偶尔会返回以小时计的等待时间，
// 照单全收等于让任务在队列里睡一整天。
const maxRetryAfter = 15 * time.Minute

// upstreamStatusError 保留原始状态码，让调用方能区分「条目不存在」和「被限流」，
// 而不是只拿到一句字符串。Error() 的文案与改造前保持一致，日志和测试都不用跟着改。
type upstreamStatusError struct {
	source string
	status int
}

func (err *upstreamStatusError) Error() string {
	return fmt.Sprintf("%s returned HTTP %d", err.source, err.status)
}

// classifyUpstreamStatus 把非 200 响应翻译成队列能理解的失败类型。
// 429 和 503 是「稍后再来」；生产环境里豆瓣对被限流/封禁的 IP 也会用 400 打发，
// 这种 400 同样是「稍后再来」而不是「请求本身有问题」，不该按普通错误消耗任务的重试预算。
func classifyUpstreamStatus(source string, response *http.Response) error {
	statusError := &upstreamStatusError{source: source, status: response.StatusCode}
	switch response.StatusCode {
	case http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return workqueue.Throttled(statusError, parseRetryAfter(response.Header.Get("Retry-After")))
	default:
		return statusError
	}
}

func upstreamStatus(err error) (int, bool) {
	var statusError *upstreamStatusError
	if errors.As(err, &statusError) {
		return statusError.status, true
	}
	return 0, false
}

// parseRetryAfter 同时接受秒数和 HTTP 日期两种写法，解析不出来时返回 0，由队列自行退避。
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return capRetryAfter(time.Duration(seconds) * time.Second)
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return capRetryAfter(time.Until(deadline))
	}
	return 0
}

func capRetryAfter(value time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	if value > maxRetryAfter {
		return maxRetryAfter
	}
	return value
}
