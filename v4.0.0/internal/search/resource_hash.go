package search

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// StableResourceHash 根据来源提供的资源内容生成确定性哈希。
// 易变化的访问时间、播放样本和规范匹配字段被刻意排除，避免日常使用制造虚假的元数据更新。
func StableResourceHash(item VodItem) string {
	payload := struct {
		SourceKey   string `json:"source_key"`
		VodID       string `json:"vod_id"`
		VodName     string `json:"vod_name"`
		VodSub      string `json:"vod_sub"`
		VodEn       string `json:"vod_en"`
		VodTag      string `json:"vod_tag"`
		VodClass    string `json:"vod_class"`
		VodPic      string `json:"vod_pic"`
		VodActor    string `json:"vod_actor"`
		VodDirector string `json:"vod_director"`
		VodBlurb    string `json:"vod_blurb"`
		VodRemarks  string `json:"vod_remarks"`
		VodPubdate  string `json:"vod_pubdate"`
		VodTotal    string `json:"vod_total"`
		VodSerial   string `json:"vod_serial"`
		VodArea     string `json:"vod_area"`
		VodLang     string `json:"vod_lang"`
		VodYear     string `json:"vod_year"`
		VodDuration string `json:"vod_duration"`
		VodTime     string `json:"vod_time"`
		DoubanID    string `json:"douban_id"`
		VodContent  string `json:"vod_content"`
		VodPlayURL  string `json:"vod_play_url"`
		TypeName    string `json:"type_name"`
	}{
		SourceKey:   hashText(item.SourceKey),
		VodID:       hashText(item.VodId),
		VodName:     hashText(item.VodName),
		VodSub:      hashText(item.VodSub),
		VodEn:       hashText(item.VodEn),
		VodTag:      hashText(item.VodTag),
		VodClass:    hashText(item.VodClass),
		VodPic:      hashText(item.VodPic),
		VodActor:    hashText(item.VodActor),
		VodDirector: hashText(item.VodDirector),
		VodBlurb:    hashText(item.VodBlurb),
		VodRemarks:  hashText(item.VodRemarks),
		VodPubdate:  hashText(item.VodPubdate),
		VodTotal:    hashText(item.VodTotal),
		VodSerial:   hashText(item.VodSerial),
		VodArea:     hashText(item.VodArea),
		VodLang:     hashText(item.VodLang),
		VodYear:     hashText(item.VodYear),
		VodDuration: hashText(item.VodDuration),
		VodTime:     hashText(item.VodTime),
		DoubanID:    hashText(item.VodDoubanId),
		VodContent:  hashText(item.VodContent),
		VodPlayURL:  hashText(item.VodPlayUrl),
		TypeName:    hashText(item.TypeName),
	}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func hashText(value string) string { return strings.TrimSpace(value) }
