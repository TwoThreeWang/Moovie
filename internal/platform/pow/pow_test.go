package pow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// vectors 是 testdata/vectors.json 的结构，由 Python hashlib 独立算出。
type vectors struct {
	Salt      string            `json:"salt"`
	Hashes    map[string]string `json:"hashes"`
	Solutions []struct {
		Bits  int    `json:"bits"`
		Nonce string `json:"nonce"`
		Hash  string `json:"hash"`
	} `json:"solutions"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var loaded vectors
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestSatisfiesMatchesIndependentVectors(t *testing.T) {
	loaded := loadVectors(t)
	for input, want := range loaded.Hashes {
		sum := sha256.Sum256([]byte(input))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("sha256(%q) = %s, want %s", input, got, want)
		}
	}
	for _, solution := range loaded.Solutions {
		if !Satisfies(loaded.Salt, solution.Nonce, solution.Bits) {
			t.Fatalf("nonce %s should satisfy %d bits", solution.Nonce, solution.Bits)
		}
		// 向量里的 nonce 是从 0 递增找到的第一个解，前一个数必然不满足。
		previous, _ := strconv.Atoi(solution.Nonce)
		for nonce := previous - 1; nonce >= 0 && nonce >= previous-50; nonce-- {
			if Satisfies(loaded.Salt, strconv.Itoa(nonce), solution.Bits) {
				t.Fatalf("nonce %d unexpectedly satisfies %d bits", nonce, solution.Bits)
			}
		}
		raw, _ := hex.DecodeString(solution.Hash)
		if LeadingZeroBits(raw) < solution.Bits {
			t.Fatalf("vector hash %s has fewer than %d zero bits", solution.Hash, solution.Bits)
		}
	}
}

func TestLeadingZeroBits(t *testing.T) {
	for _, testCase := range []struct {
		data []byte
		want int
	}{
		{[]byte{0x80}, 0}, {[]byte{0x01}, 7}, {[]byte{0x00, 0x10}, 11}, {[]byte{0x00, 0x00}, 16}, {[]byte{0x0f, 0x00}, 4},
	} {
		if got := LeadingZeroBits(testCase.data); got != testCase.want {
			t.Fatalf("LeadingZeroBits(%x) = %d, want %d", testCase.data, got, testCase.want)
		}
	}
}

// testIssuer 返回一个时钟可控、难度很低的签发器。
func testIssuer(t *testing.T, bits int) (*Issuer, *time.Time) {
	t.Helper()
	issuer, err := NewIssuer(bits)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	issuer.SetClock(func() time.Time { return now })
	return issuer, &now
}

func TestVerifyAcceptsSolvedChallengeOnce(t *testing.T) {
	issuer, now := testIssuer(t, 8)
	token, err := issuer.Issue("register")
	if err != nil {
		t.Fatal(err)
	}
	nonce := Solve(token)
	*now = now.Add(3 * time.Second)
	if err := issuer.Verify(token, nonce, "register"); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := issuer.Verify(token, nonce, "register"); !errors.Is(err, ErrReplayed) {
		t.Fatalf("replay = %v, want ErrReplayed", err)
	}
}

func TestVerifyRejections(t *testing.T) {
	issuer, now := testIssuer(t, 8)
	token, _ := issuer.Issue("login")
	nonce := Solve(token)
	wrongNonce := "0"
	for Satisfies(saltOf(token), wrongNonce, 8) {
		wrongNonce += "1"
	}

	*now = now.Add(time.Second)
	if err := issuer.Verify(token, nonce, "login"); !errors.Is(err, ErrTooFast) {
		t.Fatalf("too fast = %v", err)
	}
	*now = now.Add(2 * time.Second)

	parts := strings.Split(token, ".")
	tampered := strings.Join([]string{parts[0], parts[1], "1", parts[3], parts[4]}, ".")
	otherIssuer, _ := testIssuer(t, 8)
	foreign, _ := otherIssuer.Issue("login")

	for name, testCase := range map[string]struct {
		token, nonce, scope string
		want                error
	}{
		"missing nonce":       {token, "", "login", ErrMissing},
		"missing token":       {"", nonce, "login", ErrMissing},
		"wrong nonce":         {token, wrongNonce, "login", ErrUnsolved},
		"non numeric nonce":   {token, "12a", "login", ErrUnsolved},
		"overlong nonce":      {token, strings.Repeat("1", 17), "login", ErrUnsolved},
		"cross form":          {token, nonce, "register", ErrScope},
		"tampered bits":       {tampered, nonce, "login", ErrSignature},
		"other process":       {foreign, Solve(foreign), "login", ErrSignature},
		"garbage":             {"a.b.c", nonce, "login", ErrMalformed},
		"truncated signature": {token[:len(token)-2], nonce, "login", ErrSignature},
	} {
		if err := issuer.Verify(testCase.token, testCase.nonce, testCase.scope); !errors.Is(err, testCase.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, testCase.want)
		}
	}
	// 上面的失败都不应占用挑战，正确提交依然能通过。
	if err := issuer.Verify(token, nonce, "login"); err != nil {
		t.Fatalf("valid verify after failures: %v", err)
	}
}

func TestVerifyRejectsExpiredAndHoardedEasyChallenges(t *testing.T) {
	issuer, now := testIssuer(t, 4)
	easy, _ := issuer.Issue("feedback")
	expired, _ := issuer.Issue("feedback")
	// 难度上调后，之前签发的低难度挑战即便签名有效也不再接受。
	issuer.bits = 8
	*now = now.Add(5 * time.Second)
	if err := issuer.Verify(easy, Solve(easy), "feedback"); !errors.Is(err, ErrTooEasy) {
		t.Fatalf("hoarded easy challenge = %v", err)
	}
	*now = now.Add(TTL)
	if err := issuer.Verify(expired, Solve(expired), "feedback"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired = %v", err)
	}
}

func TestCheckFormRejectsTrapBeforeChallenge(t *testing.T) {
	issuer, now := testIssuer(t, 8)
	token, _ := issuer.Issue("feedback")
	nonce := Solve(token)
	*now = now.Add(3 * time.Second)
	form := map[string]string{TokenField: token, NonceField: nonce, TrapField: "http://spam.example"}
	if err := issuer.CheckForm(func(key string) string { return form[key] }, "feedback"); !errors.Is(err, ErrTrap) {
		t.Fatalf("trap = %v", err)
	}
	delete(form, TrapField)
	if err := issuer.CheckForm(func(key string) string { return form[key] }, "feedback"); err != nil {
		t.Fatalf("clean form = %v", err)
	}
}

func TestUsedEntriesAreBoundedAndExpire(t *testing.T) {
	issuer, now := testIssuer(t, 1)
	start := *now
	for index := 0; index < maxUsedEntries; index++ {
		issuer.used[strconv.Itoa(index)] = start.Add(time.Minute)
	}
	if err := issuer.markUsed("fresh", start.Add(TTL), start); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("full table = %v", err)
	}
	if err := issuer.markUsed("fresh", start.Add(2*time.Minute+TTL), start.Add(2*time.Minute)); err != nil {
		t.Fatalf("after expiry = %v", err)
	}
	if len(issuer.used) != 1 {
		t.Fatalf("expired entries not purged: %d", len(issuer.used))
	}
}

func TestNewIssuerRejectsBadBitsAndScope(t *testing.T) {
	if _, err := NewIssuer(0); err == nil {
		t.Fatal("bits 0 accepted")
	}
	if _, err := NewIssuer(MaxBits + 1); err == nil {
		t.Fatal("bits too high accepted")
	}
	issuer, _ := testIssuer(t, 8)
	for _, scope := range []string{"", "a.b", "Login", strings.Repeat("a", 33)} {
		if _, err := issuer.Issue(scope); err == nil {
			t.Fatalf("scope %q accepted", scope)
		}
	}
}

func saltOf(token string) string { return strings.Split(token, ".")[3] }
