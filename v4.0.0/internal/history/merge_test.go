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

// 合并结果来自 map 迭代，顺序天然随机；时间戳相同的记录必须靠 ID 兜底，
// 否则「继续观看」每次刷新都会重排。批量迁移进来的历史常常共享同一时间戳。
func TestMergeContinueOrdersDeterministicallyOnEqualTimestamps(t *testing.T) {
	same := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	records := []Record{
		{ID: 1, MediaID: 11, Episode: "1", UpdatedAt: same},
		{ID: 2, MediaID: 12, Episode: "1", UpdatedAt: same},
		{ID: 3, MediaID: 13, Episode: "1", UpdatedAt: same},
		{ID: 4, MediaID: 14, Episode: "1", UpdatedAt: same},
		{ID: 5, MediaID: 15, Episode: "1", UpdatedAt: same},
	}
	want := []int{5, 4, 3, 2, 1}
	for attempt := 0; attempt < 20; attempt++ {
		merged := MergeContinue(records)
		if len(merged) != len(want) {
			t.Fatalf("merged = %d 条, want %d", len(merged), len(want))
		}
		for index, id := range want {
			if merged[index].ID != id {
				t.Fatalf("第 %d 次合并顺序 = %d, want %d", attempt, merged[index].ID, id)
			}
		}
	}
}

func TestMergeContinuePutsNewestFirst(t *testing.T) {
	base := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	merged := MergeContinue([]Record{
		{ID: 1, MediaID: 11, Episode: "1", UpdatedAt: base},
		{ID: 2, MediaID: 12, Episode: "1", UpdatedAt: base.Add(time.Hour)},
		{ID: 3, MediaID: 13, Episode: "1", UpdatedAt: base.Add(-time.Hour)},
	})
	if merged[0].ID != 2 || merged[len(merged)-1].ID != 3 {
		t.Fatalf("顺序 = %d..%d, want 最新在前", merged[0].ID, merged[len(merged)-1].ID)
	}
}
