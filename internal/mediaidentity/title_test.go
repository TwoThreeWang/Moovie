package mediaidentity

import "testing"

func TestNormalizeTitleUsesNFKCWithoutDestroyingSeasonIdentity(t *testing.T) {
	tests := map[string]string{
		" 黑袍纠察队：第二季 ":    "黑袍纠察队第二季",
		"Season ２ / S02": "season2s02",
		"Spider-Man":     "spiderman",
	}
	for input, want := range tests {
		if got := NormalizeTitle(input); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEpisodeNumberFromKeyRejectsUnknownLabels(t *testing.T) {
	if got := episodeNumberFromKey("S02E003"); got != 3 {
		t.Fatalf("episode number = %d", got)
	}
	for _, key := range []string{"feature", "正片", "S01EX"} {
		if got := episodeNumberFromKey(key); got != 0 {
			t.Errorf("episodeNumberFromKey(%q) = %d", key, got)
		}
	}
}
