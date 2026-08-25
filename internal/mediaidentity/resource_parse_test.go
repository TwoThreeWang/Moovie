package mediaidentity

import "testing"

func TestParseResourceEpisodesPreservesLinesAndNormalizesEpisodeKeys(t *testing.T) {
	episodes := ParseResourceEpisodes("source", "42", 7, "tv",
		"第01集$https://a.example/1.m3u8#第02集$https://a.example/2.m3u8$$$第01集$https://b.example/1.m3u8")
	if len(episodes) != 3 {
		t.Fatalf("episodes = %+v", episodes)
	}
	if episodes[0].LineKey != "default" || episodes[0].EpisodeKey != "S01E01" || episodes[0].LineOrder != 0 ||
		episodes[2].LineKey != "line-02" || episodes[2].EpisodeKey != "S01E01" || episodes[2].LineOrder != 1 {
		t.Fatalf("structured episodes = %+v", episodes)
	}
}

func TestParseResourceEpisodesUsesFeatureIdentityForSingleMovieStream(t *testing.T) {
	episodes := ParseResourceEpisodes("source", "movie", 8, "电影", "正片$https://a.example/main.m3u8")
	if len(episodes) != 1 || episodes[0].UnitType != "feature" || episodes[0].LineKey != "default" {
		t.Fatalf("movie episodes = %+v", episodes)
	}
}
