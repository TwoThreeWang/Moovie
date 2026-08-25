package playback

import "testing"

func TestPlaybackHealthBetaScorePrefersReliableEvidence(t *testing.T) {
	strong := PlaybackHealth{SuccessCount: 90, FailureCount: 10, AvgLoadMs: 900}
	oneSample := PlaybackHealth{SuccessCount: 1, AvgLoadMs: 100}
	if strong.Score() <= oneSample.Score() || strong.SuccessRate() != 0.9 {
		t.Fatalf("strong/one-sample = %.4f/%.4f rate=%.2f", strong.Score(), oneSample.Score(), strong.SuccessRate())
	}
}

func TestCandidateScoreIncludesBoundedMappingConfidence(t *testing.T) {
	health := PlaybackHealth{SuccessCount: 9, FailureCount: 1, AvgLoadMs: 800}
	verified := SourceCandidate{Health: health, MappingConfidence: 1}
	unverified := SourceCandidate{Health: health, MappingConfidence: 0}
	if verified.Score() <= unverified.Score() || verified.Score() > 1 || unverified.Score() < 0 {
		t.Fatalf("verified/unverified = %.4f/%.4f", verified.Score(), unverified.Score())
	}
}

func TestRankSameEpisodeNeverMixesEpisodes(t *testing.T) {
	candidates := []SourceCandidate{
		{SourceKey: "fast", VodID: "a", SeasonNumber: 1, EpisodeKey: "S01E03", PlayURL: "fast-3", Health: PlaybackHealth{SuccessCount: 8, FailureCount: 1}},
		{SourceKey: "wrong", VodID: "b", SeasonNumber: 1, EpisodeKey: "S01E01", PlayURL: "wrong-1", Health: PlaybackHealth{SuccessCount: 100}},
		{SourceKey: "slow", VodID: "c", SeasonNumber: 1, EpisodeKey: "S01E03", PlayURL: "slow-3", Health: PlaybackHealth{SuccessCount: 3, FailureCount: 2}},
	}
	ranked := RankSameEpisode(candidates, 1, "S01E03")
	if len(ranked) != 2 || ranked[0].PlayURL != "fast-3" {
		t.Fatalf("ranked = %+v", ranked)
	}
}
