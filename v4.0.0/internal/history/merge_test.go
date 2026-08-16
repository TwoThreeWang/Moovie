package history

import (
	"testing"
	"time"
)

func TestMergeContinueKeepsSameEpisodeAcrossSources(t *testing.T) {
	base := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	records := MergeContinue([]Record{
		{ID: 1, MediaID: 9, SeasonNumber: 1, EpisodeKey: "S01E03", Source: "slow", VodID: "a", Episode: "第03集", WatchedAt: base},
		{ID: 2, MediaID: 9, SeasonNumber: 1, EpisodeKey: "S01E03", Source: "fast", VodID: "b", Episode: "S01E03", WatchedAt: base.Add(time.Minute)},
		{ID: 3, MediaID: 9, SeasonNumber: 1, EpisodeKey: "S01E01", Source: "fast", VodID: "b", Episode: "S01E01", WatchedAt: base.Add(2 * time.Minute)},
	})
	if len(records) != 2 {
		t.Fatalf("merged records = %+v", records)
	}
	if records[0].EpisodeKey != "S01E01" || records[0].ID != 3 {
		t.Fatalf("latest episode = %+v", records[0])
	}
	for _, record := range records {
		if record.EpisodeKey == "S01E03" && record.ID != 2 {
			t.Fatalf("same episode did not keep latest source: %+v", record)
		}
	}
}
