// Package pow 实现表单人机校验用的工作量证明（Proof of Work）。
//
// 规则：浏览器找一个十进制 nonce，使 SHA-256(salt + ":" + nonce) 的前 bits 位为 0。
// 正常用户只是在页面加载后多等约一秒，批量脚本则每提交一次都要付出同样的算力，
// 并且必须在页面打开 2 秒后才能提交，刷接口的成本随请求数线性增长。
//
// 挑战由服务端签发：token = scope.签发毫秒.bits.salt.HMAC签名。
// 签名密钥在进程启动时随机生成，重启后旧挑战全部失效；已用 salt 也只存在内存里，
// 两者寿命一致，不会出现「已用记录随重启清空，旧挑战又能再用一次」的情况。
// 代价是多实例部署时挑战只能回到签发它的实例校验，届时要换成共享密钥和共享存储。
package pow

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 表单字段名。陷阱字段的名字刻意不像 website、company、nickname 这类
// 浏览器或密码管理器会自动填充的字段，避免误伤真人。
const (
	TokenField = "pow_token"
	NonceField = "pow_nonce"
	TrapField  = "mv_extra_note"
)

// 挑战参数。
const (
	// DefaultBits 是平时的难度。按浏览器实测的纯 JS 哈希速度定：
	// 期望 2^20 ≈ 105 万次哈希，桌面浏览器约 0.2 秒，中端手机约 1 秒。
	DefaultBits = 20
	// MinBits/MaxBits 限定可配置的难度范围，防止配置失误让挑战失去意义或卡死浏览器。
	MinBits = 1
	MaxBits = 28
	// TTL 是挑战的有效期，覆盖用户慢慢填表的时间。
	TTL = 30 * time.Minute
	// MinSolveTime 是签发到提交的最短间隔，更快的提交视为脚本。
	// 前端会把提交拦到页面打开 2.1 秒之后，留出 100ms 余量。
	MinSolveTime = 2 * time.Second
	// maxNonceLength 限制 nonce 长度。前端从 0 递增计数，32 位以内足够。
	maxNonceLength = 16
	// maxUsedEntries 是已用挑战记录的上限。每条记录都要对方先算出一个有效解，
	// 30 分钟内攒满 20 万条意味着极大的算力投入，此时宁可拒绝也不让内存无限增长。
	maxUsedEntries = 200_000
)

// 校验错误。调用方对用户统一显示一句「安全校验失败」，这里区分开只为测试和日志。
var (
	ErrMissing    = errors.New("pow: challenge or nonce missing")
	ErrTrap       = errors.New("pow: trap field filled")
	ErrMalformed  = errors.New("pow: malformed challenge")
	ErrSignature  = errors.New("pow: bad signature")
	ErrScope      = errors.New("pow: scope mismatch")
	ErrExpired    = errors.New("pow: challenge expired")
	ErrTooFast    = errors.New("pow: submitted too fast")
	ErrTooEasy    = errors.New("pow: difficulty below current minimum")
	ErrUnsolved   = errors.New("pow: nonce does not satisfy difficulty")
	ErrReplayed   = errors.New("pow: challenge already used")
	ErrOverloaded = errors.New("pow: too many outstanding challenges")
)

// Issuer 签发并校验挑战。并发安全，整个进程共用一个即可。
type Issuer struct {
	secret []byte
	bits   int
	now    func() time.Time

	mu   sync.Mutex
	used map[string]time.Time // salt -> 过期时间
}

// NewIssuer 用随机密钥创建签发器，bits 是当前要求的最低难度。
func NewIssuer(bits int) (*Issuer, error) {
	if bits < MinBits || bits > MaxBits {
		return nil, fmt.Errorf("pow: bits %d out of range [%d,%d]", bits, MinBits, MaxBits)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("pow: generate secret: %w", err)
	}
	return &Issuer{secret: secret, bits: bits, now: time.Now, used: make(map[string]time.Time)}, nil
}

// MustNewIssuer 与 NewIssuer 相同，出错直接 panic，用于启动阶段装配。
func MustNewIssuer(bits int) *Issuer {
	issuer, err := NewIssuer(bits)
	if err != nil {
		panic(err)
	}
	return issuer
}

// SetClock 替换时钟，仅供测试使用。
func (issuer *Issuer) SetClock(now func() time.Time) { issuer.now = now }

// Bits 返回当前难度。
func (issuer *Issuer) Bits() int { return issuer.bits }

// Issue 为某个表单（scope）签发一个新挑战。scope 只能由小写字母、数字和下划线组成。
func (issuer *Issuer) Issue(scope string) (string, error) {
	if !validScope(scope) {
		return "", fmt.Errorf("pow: invalid scope %q", scope)
	}
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", fmt.Errorf("pow: generate salt: %w", err)
	}
	payload := strings.Join([]string{
		scope,
		strconv.FormatInt(issuer.now().UnixMilli(), 10),
		strconv.Itoa(issuer.bits),
		hex.EncodeToString(saltBytes),
	}, ".")
	return payload + "." + issuer.sign(payload), nil
}

// Verify 校验挑战和 nonce，成功后把该挑战标记为已用。
// 校验顺序先做纯计算的检查，最后才占用已用记录，失败的提交不会消耗挑战。
func (issuer *Issuer) Verify(token, nonce, scope string) error {
	if token == "" || nonce == "" {
		return ErrMissing
	}
	parts := strings.Split(token, ".")
	if len(parts) != 5 {
		return ErrMalformed
	}
	tokenScope, issuedText, bitsText, salt, signature := parts[0], parts[1], parts[2], parts[3], parts[4]
	expected := issuer.sign(strings.Join(parts[:4], "."))
	if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
		return ErrSignature
	}
	if tokenScope != scope {
		return ErrScope
	}
	issuedMillis, err := strconv.ParseInt(issuedText, 10, 64)
	if err != nil {
		return ErrMalformed
	}
	bits, err := strconv.Atoi(bitsText)
	if err != nil || bits < MinBits || bits > MaxBits {
		return ErrMalformed
	}
	issued := time.UnixMilli(issuedMillis)
	now := issuer.now()
	if now.Sub(issued) > TTL {
		return ErrExpired
	}
	if now.Sub(issued) < MinSolveTime {
		return ErrTooFast
	}
	// 难度只认当前要求：提前囤积的低难度挑战在难度上调后直接作废。
	if bits < issuer.bits {
		return ErrTooEasy
	}
	if !validNonce(nonce) || !Satisfies(salt, nonce, bits) {
		return ErrUnsolved
	}
	return issuer.markUsed(salt, issued.Add(TTL), now)
}

// CheckForm 是表单处理器的统一入口：先看陷阱字段，再校验挑战。
// value 传 gin 的 c.PostForm 即可。
func (issuer *Issuer) CheckForm(value func(string) string, scope string) error {
	if strings.TrimSpace(value(TrapField)) != "" {
		return ErrTrap
	}
	return issuer.Verify(strings.TrimSpace(value(TokenField)), strings.TrimSpace(value(NonceField)), scope)
}

// markUsed 原子地占用一个 salt。记录满时先清理过期条目，仍然满就拒绝。
func (issuer *Issuer) markUsed(salt string, expiry, now time.Time) error {
	issuer.mu.Lock()
	defer issuer.mu.Unlock()
	if previous, exists := issuer.used[salt]; exists && now.Before(previous) {
		return ErrReplayed
	}
	if len(issuer.used) >= maxUsedEntries {
		for key, until := range issuer.used {
			if !now.Before(until) {
				delete(issuer.used, key)
			}
		}
		if len(issuer.used) >= maxUsedEntries {
			return ErrOverloaded
		}
	}
	issuer.used[salt] = expiry
	return nil
}

// sign 计算 payload 的 HMAC-SHA256。
func (issuer *Issuer) sign(payload string) string {
	mac := hmac.New(sha256.New, issuer.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// Satisfies 判断 SHA-256(salt + ":" + nonce) 的前 bits 位是否全为 0。
// 前端 pow.js 实现的是同一个算法，两边用同一组测试向量对齐。
func Satisfies(salt, nonce string, bits int) bool {
	sum := sha256.Sum256([]byte(salt + ":" + nonce))
	return LeadingZeroBits(sum[:]) >= bits
}

// LeadingZeroBits 返回字节串开头连续 0 位的个数。
func LeadingZeroBits(data []byte) int {
	count := 0
	for _, value := range data {
		if value == 0 {
			count += 8
			continue
		}
		for mask := byte(0x80); mask != 0 && value&mask == 0; mask >>= 1 {
			count++
		}
		break
	}
	return count
}

// validScope 限定 scope 字符集，保证它不含分隔符。
func validScope(scope string) bool {
	if scope == "" || len(scope) > 32 {
		return false
	}
	for _, character := range scope {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_') {
			return false
		}
	}
	return true
}

// validNonce 只接受不超过 16 位的十进制数字串。
func validNonce(nonce string) bool {
	if nonce == "" || len(nonce) > maxNonceLength {
		return false
	}
	for _, character := range nonce {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
