package playback

import "context"

// AdFingerprint 是一条广告指纹的众包投票记录。
type AdFingerprint struct {
	Fingerprint  []byte `json:"fingerprint"`
	ConfirmCount int    `json:"confirm_count"`
	RejectCount  int    `json:"reject_count"`
}

const (
	minConfirmCount    = 5
	minConfirmRatio    = 0.80
	minRejectCount     = 3
	maxAutoSkipSeconds = 120
)

// Status 返回指纹的众包状态。
func (f AdFingerprint) Status() string {
	if f.ConfirmCount >= minConfirmCount {
		ratio := float64(f.ConfirmCount) / float64(f.ConfirmCount+f.RejectCount)
		if ratio >= minConfirmRatio {
			return "confirmed"
		}
	}
	if f.RejectCount >= minRejectCount && f.RejectCount > f.ConfirmCount {
		return "rejected"
	}
	if f.ConfirmCount == 0 && f.RejectCount == 0 {
		return "unknown"
	}
	return "pending"
}

// AdFingerprintStore 是广告指纹的读写接口。
type AdFingerprintStore interface {
	MatchFingerprints(ctx context.Context, fingerprints [][]byte) ([]AdFingerprint, error)
	VoteConfirm(ctx context.Context, fingerprint []byte) (*AdFingerprint, error)
	VoteReject(ctx context.Context, fingerprint []byte) (*AdFingerprint, error)
}
