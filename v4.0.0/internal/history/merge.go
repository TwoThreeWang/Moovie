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
	// result 来自 map 迭代，顺序本身是随机的，SliceStable 的「稳定」保的是这份
	// 随机顺序，时间戳相同的记录每次刷新都会换位置。批量迁移进来的历史常常共享
	// 同一个时间戳，所以必须用 ID 兜底，让排序完全确定。
	sort.Slice(result, func(i, j int) bool {
		left, right := recordTime(result[i]), recordTime(result[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return result[i].ID > result[j].ID
	})
	return result
}

// recordTime 取记录的有效时间，优先 UpdatedAt。
func recordTime(record Record) (value time.Time) {
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt
	}
	return record.WatchedAt
}

// itoa 是不依赖 strconv 的整数转字符串。
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
