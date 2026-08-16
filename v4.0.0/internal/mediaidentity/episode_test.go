package mediaidentity

import "testing"

func TestNormalizeEpisodeLabelKeepsSeasonIdentity(t *testing.T) {
	tests := []struct {
		label, key string
		season     int
	}{
		{"S02E03", "S02E03", 2},
		{"第2季第3集", "S02E03", 2},
		{"第03集", "S01E03", 1},
		{"EP 4", "S01E04", 1},
		{"", "S01E01", 1},
	}
	for _, test := range tests {
		season, key := NormalizeEpisodeLabel(test.label)
		if season != test.season || key != test.key {
			t.Errorf("NormalizeEpisodeLabel(%q) = %d/%q, want %d/%q", test.label, season, key, test.season, test.key)
		}
	}
}
