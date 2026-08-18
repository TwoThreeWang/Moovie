package recommendation

import (
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestGenerateReasonPreservesPriorityAndSimilarityDimensions(t *testing.T) {
	source := catalog.Movie{Title: "银翼杀手", Year: "1982", Rating: 8.6, Genres: "科幻,犯罪", Directors: `[{"name":"雷德利·斯科特"}]`, Actors: `[{"name":"哈里森·福特"}]`}
	target := catalog.Movie{Title: "银翼杀手2049", Year: "2017", Rating: 8.3, Genres: "科幻,犯罪", Directors: `[{"name":"丹尼斯"}]`, Actors: `[{"name":"哈里森·福特"}]`}
	reason, reasonType, score := GenerateReason(source, target)
	if reasonType != "actor" || !strings.Contains(reason, "哈里森·福特") || score <= 0.6 {
		t.Fatalf("reason/type/score = %q/%q/%f", reason, reasonType, score)
	}

	directorTarget := catalog.Movie{Title: "异形", Year: "1979", Rating: 8.3, Genres: "科幻,惊悚", Directors: source.Directors}
	reason, reasonType, _ = GenerateReason(source, directorTarget)
	if reasonType != "director" || !strings.Contains(reason, "雷德利·斯科特") {
		t.Fatalf("director reason/type = %q/%q", reason, reasonType)
	}
}

func TestServiceReturnsSourceAndReasonedMovies(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := catalog.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), catalog.Movie{DoubanID: "source", Title: "源电影", Genres: "剧情", Rating: 9})
	_ = store.Upsert(t.Context(), catalog.Movie{DoubanID: "target", Title: "目标电影", Genres: "剧情", Rating: 8.8})
	seedEmbedding(t, store, "source")
	seedEmbedding(t, store, "target")
	result, source, err := NewService(store).FindSimilarWithReasons(t.Context(), "source", 8)
	if err != nil || source == nil || source.Title != "源电影" || len(result) != 1 || result[0].Movie.DoubanID != "target" || result[0].Reason == "" {
		t.Fatalf("result/source/error = %+v/%+v/%v", result, source, err)
	}
}
