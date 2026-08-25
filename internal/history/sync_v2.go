package history

import (
	"fmt"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
)

// 同步接口的边界：一次最多 100 条操作，一次最多返回 500 条变更，
// 客户端时钟比服务端快 5 分钟以内的时间戳按有效处理。
const (
	maxSyncOperations = 100
	maxClockSkew      = 5 * time.Minute
)

// SyncV2Request 是客户端提交的同步请求：本机待上传的操作 + 已收到的游标。
type SyncV2Request struct {
	DeviceID   string          `json:"device_id"`
	Cursor     int64           `json:"cursor"`
	Operations []SyncOperation `json:"operations"`
}

// SyncOperation 是一条同步操作。operation_id 由客户端生成并保证唯一，服务端靠它做幂等。
// force 是内部字段（不走 JSON），仅供服务端自己发起的删除跳过时间冲突检查。
type SyncOperation struct {
	OperationID string    `json:"operation_id"`
	DeviceSeq   int64     `json:"device_seq"`
	Type        string    `json:"type"`
	HistoryID   int       `json:"history_id,omitempty"`
	MediaID     int       `json:"media_id,omitempty"`
	MediaUnitID int       `json:"media_unit_id,omitempty"`
	DoubanID    string    `json:"douban_id,omitempty"`
	VodID       string    `json:"vod_id,omitempty"`
	Source      string    `json:"source_key,omitempty"`
	Title       string    `json:"title,omitempty"`
	Poster      string    `json:"poster,omitempty"`
	Episode     string    `json:"episode,omitempty"`
	Season      int       `json:"season_number,omitempty"`
	EpisodeKey  string    `json:"episode_key,omitempty"`
	Position    float64   `json:"position_seconds,omitempty"`
	Duration    float64   `json:"duration_seconds,omitempty"`
	Progress    int       `json:"progress_percent,omitempty"`
	EntryPage   string    `json:"entry_page,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
	force       bool
}

// SyncV2Result 是同步返回：新游标、增量变更、冲突列表。
// RequiresFullSync 表示客户端游标比服务端还大（通常是换库或数据被清过），需要全量重来。
type SyncV2Result struct {
	Cursor           int64          `json:"cursor"`
	Changes          []SyncChange   `json:"changes"`
	Conflicts        []SyncConflict `json:"conflicts"`
	RequiresFullSync bool           `json:"requires_full_sync"`
}

// SyncChange 是一条已生效的变更。
type SyncChange struct {
	Version     int64   `json:"version"`
	OperationID string  `json:"operation_id"`
	Type        string  `json:"type"`
	Record      *Record `json:"record,omitempty"`
}

// SyncConflict 表示这条操作没生效（服务端记录更新），并附上服务端的当前值。
type SyncConflict struct {
	Version     int64   `json:"version"`
	OperationID string  `json:"operation_id"`
	Reason      string  `json:"reason"`
	Current     *Record `json:"current,omitempty"`
}

// syncEventPayload 是事件账本里存的 JSON 内容。
type syncEventPayload struct {
	Change   *SyncChange   `json:"change,omitempty"`
	Conflict *SyncConflict `json:"conflict,omitempty"`
}

// normalizeSyncRequest 校验并归一化同步请求：ID 长度、操作类型、去重、季集补全、
// 进度百分比换算、时间戳纠偏。校验不通过直接 400，不写库。
func normalizeSyncRequest(request *SyncV2Request, now time.Time) error {
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	if len(request.DeviceID) < 8 || len(request.DeviceID) > 128 {
		return fmt.Errorf("device_id must contain 8 to 128 characters")
	}
	if request.Cursor < 0 {
		return fmt.Errorf("cursor must not be negative")
	}
	if len(request.Operations) > maxSyncOperations {
		return fmt.Errorf("operations exceeds %d entries", maxSyncOperations)
	}
	seen := make(map[string]struct{}, len(request.Operations))
	for index := range request.Operations {
		operation := &request.Operations[index]
		operation.OperationID = strings.TrimSpace(operation.OperationID)
		operation.Type = strings.ToLower(strings.TrimSpace(operation.Type))
		operation.Source = strings.TrimSpace(operation.Source)
		operation.VodID = strings.TrimSpace(operation.VodID)
		operation.Episode = strings.TrimSpace(operation.Episode)
		operation.EpisodeKey = strings.TrimSpace(operation.EpisodeKey)
		operation.EntryPage = strings.ToLower(strings.TrimSpace(operation.EntryPage))
		if operation.EntryPage == "" {
			operation.EntryPage = "play"
		}
		if operation.EntryPage != "play" && operation.EntryPage != "watch" {
			return fmt.Errorf("operations[%d].entry_page is invalid", index)
		}
		if len(operation.OperationID) < 8 || len(operation.OperationID) > 128 {
			return fmt.Errorf("operations[%d].operation_id must contain 8 to 128 characters", index)
		}
		if _, duplicate := seen[operation.OperationID]; duplicate {
			return fmt.Errorf("operations[%d].operation_id is duplicated", index)
		}
		seen[operation.OperationID] = struct{}{}
		if operation.DeviceSeq < 0 {
			return fmt.Errorf("operations[%d].device_seq must not be negative", index)
		}
		switch operation.Type {
		case "upsert", "delete", "complete":
		default:
			return fmt.Errorf("operations[%d].type is invalid", index)
		}
		if operation.Season < 1 || operation.EpisodeKey == "" {
			season, episodeKey := mediaidentity.NormalizeEpisodeLabel(operation.Episode)
			if operation.Season < 1 {
				operation.Season = season
			}
			if operation.EpisodeKey == "" {
				operation.EpisodeKey = episodeKey
			}
		}
		if operation.OccurredAt.IsZero() || operation.OccurredAt.After(now.Add(maxClockSkew)) {
			operation.OccurredAt = now
		}
		if operation.Position < 0 {
			operation.Position = 0
		}
		if operation.Duration < 0 {
			operation.Duration = 0
		}
		if operation.Duration > 0 {
			operation.Progress = int(operation.Position * 100 / operation.Duration)
		}
		if operation.Type == "complete" {
			operation.Progress = 100
			if operation.Duration > 0 {
				operation.Position = operation.Duration
			}
		}
		if operation.Progress < 0 {
			operation.Progress = 0
		}
		if operation.Progress > 100 {
			operation.Progress = 100
		}
		if operation.Type != "delete" && (operation.Source == "" || operation.VodID == "") {
			return fmt.Errorf("operations[%d] requires source_key and vod_id", index)
		}
		if operation.Type == "delete" && operation.HistoryID <= 0 && operation.MediaID <= 0 && (operation.Source == "" || operation.VodID == "") {
			return fmt.Errorf("operations[%d] has no deletable identity", index)
		}
	}
	return nil
}

// recordFromOperation 把同步操作转成进度记录。
func recordFromOperation(userID int, operation SyncOperation) Record {
	return Record{
		ID: operation.HistoryID, UserID: userID, MediaID: operation.MediaID,
		MediaUnitID: operation.MediaUnitID,
		DoubanID:    operation.DoubanID, VodID: operation.VodID, Source: operation.Source,
		Title: operation.Title, Poster: operation.Poster, Episode: operation.Episode,
		SeasonNumber: operation.Season, EpisodeKey: operation.EpisodeKey,
		Progress: operation.Progress, LastTime: operation.Position, Duration: operation.Duration,
		PreferredSource: operation.Source, PreferredVodID: operation.VodID,
		EntryPage: operation.EntryPage,
		WatchedAt: operation.OccurredAt, UpdatedAt: operation.OccurredAt,
	}
}

// operationMatchesRecord 判断一条操作指向的是不是这条记录：
// 依次按记录 ID、季集 ID、媒体+季集、资源站+资源 ID 匹配。
func operationMatchesRecord(operation SyncOperation, record Record) bool {
	if operation.HistoryID > 0 && record.ID == operation.HistoryID {
		return true
	}
	if operation.MediaUnitID > 0 && record.MediaUnitID == operation.MediaUnitID {
		return true
	}
	if operation.MediaID > 0 && operation.EpisodeKey != "" && record.MediaID == operation.MediaID &&
		record.SeasonNumber == operation.Season && record.EpisodeKey == operation.EpisodeKey {
		return true
	}
	return operation.Source != "" && operation.VodID != "" &&
		record.Source == operation.Source && record.VodID == operation.VodID
}
