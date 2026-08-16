package history

import (
	"sort"
	"time"
)

// MergeContinue 只有在规范媒体身份已知时才合并重复资源行。
// 只有资源身份的记录仍按 source/vod 区分，避免把两部无关作品合在一起。
func MergeContinue(records []Record) []Record {
	merged := make(map[string]Record, len(records))
	for _, record := range records {
		key := record.Source + "\x00" + record.VodID + "\x00" + record.Episode
		if record.MediaUnitID > 0 {
			key = "unit:" + itoa(record.MediaUnitID)
		} else if record.MediaID > 0 {
			episodeKey := record.EpisodeKey
			if episodeKey == "" {
				episodeKey = record.Episode
			}
			key = "media:" + itoa(record.MediaID) + ":" + itoa(record.SeasonNumber) + ":" + episodeKey
		}
		current, exists := merged[key]
		if !exists || recordTime(record).After(recordTime(current)) ||
			(recordTime(record).Equal(recordTime(current)) && record.Progress > current.Progress) {
			merged[key] = record
		}
	}
	result := make([]Record, 0, len(merged))
	for _, record := range merged {
		result = append(result, record)
	}
	sort.SliceStable(result, func(i, j int) bool { return recordTime(result[i]).After(recordTime(result[j])) })
	return result
}

func recordTime(record Record) (value time.Time) {
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt
	}
	return record.WatchedAt
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := [20]byte{}
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buffer[index] = '-'
	}
	return string(buffer[index:])
}
