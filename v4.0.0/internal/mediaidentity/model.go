package mediaidentity

import "time"

// Media 是资料、资源和历史共同引用的规范媒体身份。
// 来源专属元数据仍保存在各自表中；该模型只保留识别和展示作品所需的稳定字段。
//
// SeriesStatus 是 TMDB 的连载状态原值（Returning Series / Ended / Canceled 等），
// 空串表示未知；电影没有这个概念。判断是否还会更新请用 SeriesEnded，不要直接比较字符串。
type Media struct {
	ID                 int
	MediaType          string
	DoubanID           string
	Title              string
	OriginalTitle      string
	Year               string
	Poster             string
	Backdrops          string
	Summary            string
	Genres             string
	Countries          string
	Directors          string
	Actors             string
	Duration           string
	RatingDouban       float64
	RatingTMDB         float64
	VoteCountTMDB      int
	SeriesStatus       string
	MetadataVersion    int
	MetadataStatus     string
	LastMetadataSyncAt time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ExternalID struct {
	MediaID      int
	Provider     string
	ExternalType string
	ExternalID   string
	Confidence   float64
	IsPrimary    bool
	VerifiedAt   time.Time
}

type Alias struct {
	MediaID         int
	Alias           string
	NormalizedAlias string
	Language        string
	Source          string
	AliasType       string
}

type MediaUnit struct {
	ID             int
	MediaID        int
	UnitType       string
	SeasonNumber   int
	EpisodeNumber  int
	AbsoluteNumber int
	EpisodeKey     string
	Title          string
	AirDate        time.Time
	RuntimeMinutes int
}

type ResourceLink struct {
	SourceKey  string
	VodID      string
	MediaID    int
	Confidence float64
	MatchedBy  string
	IsLocked   bool
	VerifiedAt time.Time
}

type Episode struct {
	CandidateID    int
	LineID         int
	LineKey        string
	LineLabel      string
	LineOrder      int
	SourceKey      string
	VodID          string
	MediaID        int
	MediaUnitID    int
	UnitType       string
	SeasonNumber   int
	EpisodeKey     string
	EpisodeLabel   string
	PlayURL        string
	SortOrder      int
	Format         string
	Quality        string
	ResourceStatus string
	LastSeenAt     time.Time
	LastAccessedAt time.Time
}

type PlaybackAttemptEvent struct {
	AttemptID          string
	CandidateSessionID string
	EventType          string
	CandidateID        int
	MediaUnitID        int
	SourceKey          string
	VodID              string
	ElapsedMs          int
	Reason             string
}

type ResourceCandidate struct {
	Episode
	SuccessCount      int
	FailureCount      int
	AvgLoadMs         int
	MappingConfidence float64
}
