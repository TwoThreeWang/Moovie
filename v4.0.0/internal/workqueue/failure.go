package workqueue

import (
	"errors"
	"time"
)

// Outcome 描述一次失败该如何收尾。默认按指数退避重试，但把两类错误单独拎出来：
// 重试永远不会成功的（条目不存在、参数非法、依赖未配置），以及问题不在这个任务
// 本身的上游限流。这两类共用一套退避阶梯时，限流风暴会在几小时内把整批任务判死。
type Outcome int

const (
	OutcomeRetry Outcome = iota
	OutcomeTerminal
	OutcomeThrottled
)

// maxThrottleAttempts 是限流重试的兜底上限。限流不消耗 attempt，所以必须另有一个
// 计数器兜底，否则上游永久 429 会让任务无限期占用队列。
const maxThrottleAttempts = 20

// Failure 是 Dispatcher 交给 Store 的失败收尾指令。
type Failure struct {
	Message    string
	Outcome    Outcome
	RetryAfter time.Duration
}

type terminalError struct{ err error }

func (wrapper *terminalError) Error() string { return wrapper.err.Error() }
func (wrapper *terminalError) Unwrap() error { return wrapper.err }

// Terminal 标记一个重试也不会变好的错误，任务会直接进入 failed，不再占用重试预算。
func Terminal(err error) error {
	if err == nil {
		return nil
	}
	return &terminalError{err: err}
}

func IsTerminal(err error) bool {
	var terminal *terminalError
	return errors.As(err, &terminal)
}

type throttleError struct {
	err        error
	retryAfter time.Duration
}

func (wrapper *throttleError) Error() string { return wrapper.err.Error() }
func (wrapper *throttleError) Unwrap() error { return wrapper.err }

// Throttled 标记上游限流。retryAfter 可以来自 Retry-After 响应头；传 0 时由队列
// 按该任务已经被限流的次数决定退避时长。
func Throttled(err error, retryAfter time.Duration) error {
	if err == nil {
		return nil
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	return &throttleError{err: err, retryAfter: retryAfter}
}

// RetryAfter 取出限流错误携带的建议等待时长，供调用方同步冷却本地限流器。
func RetryAfter(err error) (time.Duration, bool) {
	var throttle *throttleError
	if errors.As(err, &throttle) {
		return throttle.retryAfter, true
	}
	return 0, false
}

func IsThrottled(err error) bool {
	_, ok := RetryAfter(err)
	return ok
}

// Classify 把 handler 返回的错误翻译成失败收尾指令。终止优先于限流：
// 一个既不存在又恰好撞上限流的条目，没有理由继续排队。
func Classify(err error) Failure {
	if err == nil {
		return Failure{}
	}
	failure := Failure{Message: err.Error(), Outcome: OutcomeRetry}
	if IsTerminal(err) {
		failure.Outcome = OutcomeTerminal
		return failure
	}
	var throttle *throttleError
	if errors.As(err, &throttle) {
		failure.Outcome = OutcomeThrottled
		failure.RetryAfter = throttle.retryAfter
	}
	return failure
}

// ThrottleBackoff 是没有 Retry-After 时的限流退避阶梯：从半分钟起步，最长十五分钟。
// 上游限流通常几分钟内恢复，用小时级阶梯等于把队列白白闲置。
func ThrottleBackoff(throttleCount int) time.Duration {
	if throttleCount < 0 {
		throttleCount = 0
	}
	backoff := 30 * time.Second << min(throttleCount, 5)
	if backoff > 15*time.Minute {
		backoff = 15 * time.Minute
	}
	return backoff
}
