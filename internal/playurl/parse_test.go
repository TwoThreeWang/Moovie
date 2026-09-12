package playurl

import (
	"strings"
	"testing"
)

func TestCleanFiltersLabelsAndFormatsWithoutInspectingURL(t *testing.T) {
	raw := "TC国语$https://a.example/tc.m3u8#预告$https://a.example/trailer.m3u8#花絮$https://a.example/extra.m3u8#HD中字$https://a.example/tc/main.m3u8?token=x$y$$$正片$https://a.example/movie.mp4$$$正片$https://b.example/main.m3u8"
	got := Clean(raw, "TC")
	want := "HD中字$https://a.example/tc/main.m3u8?token=x$y"
	if got != want || Clean(got) != got {
		t.Fatalf("clean=%q", got)
	}
	for _, raw := range []string{"javascript:alert(1)", "正片$https://a.example/page?url=file.m3u8", "正片$/local.m3u8"} {
		if Clean(raw) != "" {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestContentIdentitiesSeparateVersionsPartsDatesAndSeasons(t *testing.T) {
	movie := Entries("720P$https://a.example/1.m3u8#HD$https://a.example/2.m3u8#上$https://a.example/up.m3u8#下$https://a.example/down.m3u8", "movie", "")
	if len(movie) != 4 {
		t.Fatal(movie)
	}
	for _, e := range movie {
		if e.EpisodeKey != "feature" || e.Season != 0 {
			t.Fatal(e)
		}
	}
	if movie[2].Part == movie[3].Part || movie[0].Key == movie[1].Key {
		t.Fatal("lost identity")
	}
	show := Entries("第5期上$https://a.example/a.m3u8#第5期下$https://a.example/b.m3u8#2024-03-15$https://a.example/c.m3u8", "show", "节目第二季")
	if len(show) != 3 || show[0].EpisodeKey == show[1].EpisodeKey || show[2].EpisodeKey == show[0].EpisodeKey {
		t.Fatal(show)
	}
	tv := Entries("第3集$https://a.example/3.m3u8", "tv", "作品第二季")
	if tv[0].Season != 2 || tv[0].EpisodeKey != "S02E03" {
		t.Fatal(tv)
	}
	anime := Entries("HD$https://a.example/a.m3u8", "cartoon", "")
	if anime[0].UnitType != "feature" {
		t.Fatal(anime)
	}
	anime = Entries("第1集$https://a.example/a.m3u8", "cartoon", "")
	if anime[0].UnitType != "episode" {
		t.Fatal(anime)
	}
}

func BenchmarkParseLargeResource(b *testing.B) {
	raw := strings.Repeat("第1集$https://video.example/1.m3u8#", 1000)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		Entries(raw, "tv", "")
	}
}
