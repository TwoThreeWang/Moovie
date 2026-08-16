package mediaidentity

import (
	"strings"
	"testing"
)

func TestScoreResourceMatchExplainsWeightedExactMovieMatch(t *testing.T) {
	result := ScoreResourceMatch(MatchInput{
		Title: "肖申克的救赎 1080P 中字", OriginalTitle: "The Shawshank Redemption", Year: "1994",
		MediaType: "电影", Actors: "蒂姆·罗宾斯, 摩根·弗里曼", Directors: "弗兰克·德拉邦特",
	}, Media{ID: 7, Title: "肖申克的救赎", OriginalTitle: "The Shawshank Redemption", Year: "1994",
		MediaType: "movie", Actors: "蒂姆·罗宾斯 / 摩根·弗里曼", Directors: "弗兰克·德拉邦特"})
	if result.MediaID != 7 || result.Confidence != 1 || result.Status != "review" || result.HardConflict != "" {
		t.Fatalf("exact match = %+v", result)
	}
	for _, feature := range []string{`"title"`, `"year"`, `"media_type"`, `"season"`, `"people"`, `"original_title"`} {
		if !strings.Contains(result.ReasonJSON, feature) {
			t.Fatalf("reason missing %s: %s", feature, result.ReasonJSON)
		}
	}
}

func TestScoreResourceMatchKeepsSeasonIdentityAndRejectsConflict(t *testing.T) {
	matching := ScoreResourceMatch(MatchInput{Title: "黑袍纠察队 Season ２", Year: "2026", MediaType: "电视剧"},
		Media{ID: 8, Title: "黑袍纠察队 第二季", Year: "2026", MediaType: "tv"})
	if matching.HardConflict != "" {
		t.Fatalf("equivalent season formats conflicted: %+v", matching)
	}
	conflicting := ScoreResourceMatch(MatchInput{Title: "黑袍纠察队 S02", Year: "2026", MediaType: "电视剧"},
		Media{ID: 9, Title: "黑袍纠察队 第三季", Year: "2026", MediaType: "tv"})
	if conflicting.Status != "rejected" || !strings.Contains(conflicting.HardConflict, "season_mismatch") {
		t.Fatalf("season conflict = %+v", conflicting)
	}
}

func TestScoreResourceMatchRejectsYearAndMediaTypeConflicts(t *testing.T) {
	result := ScoreResourceMatch(MatchInput{Title: "同名作品", Year: "2026", MediaType: "电影"},
		Media{ID: 10, Title: "同名作品", Year: "2020", MediaType: "tv"})
	if result.Status != "rejected" || !strings.Contains(result.HardConflict, "year_mismatch") || !strings.Contains(result.HardConflict, "media_type_mismatch") {
		t.Fatalf("hard conflicts = %+v", result)
	}
}

func TestMatchTitlePartsStripsReleaseTagsWithoutDroppingSeason(t *testing.T) {
	title, season := matchTitleParts(" 黑袍纠察队：第十二季 2160P 修复版 ")
	if title != "黑袍纠察队" || season != 12 {
		t.Fatalf("title parts = %q/%d", title, season)
	}
	title, season = matchTitleParts("The Boys Season ２ WEB-DL")
	if title != "theboys" || season != 2 {
		t.Fatalf("full-width season parts = %q/%d", title, season)
	}
}

func TestTitleSeasonNumberUsesFirstExplicitSeason(t *testing.T) {
	if got := TitleSeasonNumber("末日地堡 第二季", "Silo Season 2"); got != 2 {
		t.Fatalf("Chinese title season = %d, want 2", got)
	}
	if got := TitleSeasonNumber("末日地堡", "Silo Season 3"); got != 3 {
		t.Fatalf("original title season = %d, want 3", got)
	}
	if got := TitleSeasonNumber("末日地堡", "Silo"); got != 0 {
		t.Fatalf("series title season = %d, want 0", got)
	}
	if got := TitleBase("Silo Season 3", "末日地堡 第三季"); got != "silo" {
		t.Fatalf("series title base = %q, want silo", got)
	}
	if got := TitleBase("The Boys Season 2 WEB-DL"); got != "the boys" {
		t.Fatalf("multi-word series title base = %q, want the boys", got)
	}
}
