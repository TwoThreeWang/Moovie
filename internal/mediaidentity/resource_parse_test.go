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

// 电影的"分集"其实是清晰度/语言版本，必须全部归到同一个正片键，原标签降级成 quality；
// 少一条就是丢了一个可选版本，键没归一就会在选集网格里冒出一排清晰度按钮。
func TestParseResourceEpisodesCollapsesMovieQualityVariants(t *testing.T) {
	episodes := ParseResourceEpisodes("source", "movie", 8, "movie",
		"720P$https://a.example/720.m3u8#HD中字$https://a.example/hd.m3u8#TC国语$https://a.example/tc.m3u8")
	if len(episodes) != 2 {
		t.Fatalf("movie episodes = %+v", episodes)
	}
	for index, wantQuality := range []string{"720P", "HD中字"} {
		got := episodes[index]
		if got.UnitType != "feature" || got.SeasonNumber != 0 || got.EpisodeKey != FeatureEpisodeKey {
			t.Fatalf("变体 %d 没有折叠成正片单元：%+v", index, got)
		}
		if got.Quality != wantQuality || got.EpisodeLabel != wantQuality {
			t.Fatalf("变体 %d quality = %q, want %q", index, got.Quality, wantQuality)
		}
	}
}

// 剧集绝不能走折叠分支：一部剧的所有集被并成一个单元是灾难性的。
func TestParseResourceEpisodesKeepsSeriesEpisodesApart(t *testing.T) {
	episodes := ParseResourceEpisodes("source", "42", 7, "国产剧",
		"第01集$https://a.example/1.m3u8#第02集$https://a.example/2.m3u8")
	if len(episodes) != 2 || episodes[0].EpisodeKey == episodes[1].EpisodeKey {
		t.Fatalf("series episodes = %+v", episodes)
	}
	if episodes[0].UnitType != "episode" || episodes[0].Quality != "" {
		t.Fatalf("剧集不该带版本标签：%+v", episodes[0])
	}
}

func TestIsQualityVariantLabelSeparatesVersionsFromEpisodes(t *testing.T) {
	for _, label := range []string{"正片", "HD", "TC", "720P", "1080P", "HD中字", "TC国语", "HD国语",
		"抢先版", "蓝光原盘版", "高清中英双语", "BD-1080P", "4K原声"} {
		if !isQualityVariantLabel(label) {
			t.Errorf("%q 应该识别为清晰度/版本标签", label)
		}
	}
	// 综艺按期、剧集按集、日期命名的，一律不能当成版本变体。
	for _, label := range []string{"第01集", "第5期", "20240315", "2024-03-15", "EP01", "S01E02",
		"第一集", "预告 第2期", "上", "下"} {
		if isQualityVariantLabel(label) {
			t.Errorf("%q 是集次，不该识别为版本标签", label)
		}
	}
}

// 豆瓣按 movie/tv/show 顺序试端点，综艺常被错标成 movie。
// 只要标签是期数，就绝不能折叠——整季并成一集是不可接受的。
func TestParseResourceEpisodesKeepsShowEpisodesWhenMediaTypeIsWrong(t *testing.T) {
	episodes := ParseResourceEpisodes("source", "show", 9, "movie",
		"第1期$https://a.example/1.m3u8#第2期$https://a.example/2.m3u8#20240315$https://a.example/3.m3u8")
	if len(episodes) != 3 {
		t.Fatalf("show episodes = %+v", episodes)
	}
	seen := map[string]bool{}
	for _, episode := range episodes {
		if episode.UnitType != "episode" || episode.Quality != "" {
			t.Fatalf("综艺被当成了正片版本：%+v", episode)
		}
		seen[episode.EpisodeKey] = true
	}
	if len(seen) != 3 {
		t.Fatalf("综艺的三期被并到了 %d 个键：%+v", len(seen), episodes)
	}
}
