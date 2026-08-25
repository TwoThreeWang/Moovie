package playback

import "testing"

func TestFormatTVBoxPlayURLMatchesStableM3U8Rules(t *testing.T) {
	playFrom, playURL := formatTVBoxPlayURL("第01集$https://a.example/1.m3u8#下载$https://a.example/file.mp4$$$正片$https://b.example/main.m3u8")
	if playFrom != "默认源$$$备用源 B" {
		t.Fatalf("playFrom = %q", playFrom)
	}
	if playURL != "第01集$https://a.example/1.m3u8$$$正片$https://b.example/main.m3u8" {
		t.Fatalf("playURL = %q", playURL)
	}
}

func TestFormatTVBoxPlayURLRejectsNonHLS(t *testing.T) {
	playFrom, playURL := formatTVBoxPlayURL("正片$https://a.example/file.mp4")
	if playFrom != "" || playURL != "" {
		t.Fatalf("non-HLS result = %q/%q", playFrom, playURL)
	}
}

func TestSelectEpisodeMatchesNormalizedLabelWithoutFallingBack(t *testing.T) {
	episodes := []PlayEpisode{{Title: "第1集", URL: "one"}, {Title: "第3集", URL: "three"}}
	selected, ok := selectEpisode(episodes, "S01E03")
	if !ok || selected.URL != "three" {
		t.Fatalf("selected = %+v, ok=%v", selected, ok)
	}
	if _, ok := selectEpisode(episodes, "S01E02"); ok {
		t.Fatal("unknown episode unexpectedly selected a fallback")
	}
}
