package playback

import (
	"sort"
	"time"
)

// PlaybackHealth 汇总用户播放样本，供线路排序与运维指标共同使用。
// 成功和失败计数分开保存，单次失败不能静默抹掉以前的成功证据。
type PlaybackHealth struct {
	SuccessCount int
	FailureCount int
	AvgLoadMs    int
	LastSuccess  time.Time
	LastFailure  time.Time
}

func (health PlaybackHealth) Total() int { return health.SuccessCount + health.FailureCount }

func (health PlaybackHealth) SuccessRate() float64 {
	if health.Total() == 0 {
		return 0
	}
	return float64(health.SuccessCount) / float64(health.Total())
}

// Score 使用 Beta(2,2) 先验并逐步增加对观测证据的信任。
// 这样单样本来源不会超过经过充分验证的候选，同时未知候选仍可作为后备。
func (health PlaybackHealth) Score() float64 {
	total := health.Total()
	reliability := 0.5
	if total > 0 {
		posterior := float64(health.SuccessCount+2) / float64(total+4)
		confidence := float64(total) / 20
		if confidence > 1 {
			confidence = 1
		}
		reliability += (posterior - 0.5) * confidence
	}
	speed := 0.0
	if health.AvgLoadMs > 0 {
		speed = 1 / (1 + float64(health.AvgLoadMs)/5000)
	}
	return reliability*0.8 + speed*0.2
}

type SourceCandidate struct {
	CandidateID       int
	LineID            int
	LineKey           string
	LineLabel         string
	SourceKey         string
	VodID             string
	MediaID           int
	MediaUnitID       int
	SeasonNumber      int
	EpisodeKey        string
	EpisodeLabel      string
	PlayURL           string
	MappingConfidence float64
	Health            PlaybackHealth
}

func (candidate SourceCandidate) Score() float64 {
	confidence := candidate.MappingConfidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return candidate.Health.Score()*0.85 + confidence*0.15
}

// RankSameEpisode 只排序请求的规范剧集候选。其他剧集会在排序前被剔除，
// 从根本上防止“第三集失败后换源打开第一集”。
func RankSameEpisode(candidates []SourceCandidate, season int, episodeKey string) []SourceCandidate {
	result := filterSameEpisode(candidates, season, episodeKey)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i].Score(), result[j].Score()
		if left == right {
			return result[i].Health.AvgLoadMs < result[j].Health.AvgLoadMs
		}
		return left > right
	})
	return result
}

func filterSameEpisode(candidates []SourceCandidate, season int, episodeKey string) []SourceCandidate {
	if season < 1 {
		season = 1
	}
	result := make([]SourceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidateSeason := candidate.SeasonNumber
		if candidateSeason < 1 {
			candidateSeason = 1
		}
		if candidateSeason != season || candidate.EpisodeKey != episodeKey || candidate.PlayURL == "" {
			continue
		}
		result = append(result, candidate)
	}
	return result
}
