package history

import "time"

type Record struct {
	ID              int       `json:"id"`
	UserID          int       `json:"user_id"`
	MediaID         int       `json:"media_id,omitempty"`
	MediaUnitID     int       `json:"media_unit_id,omitempty"`
	DoubanID        string    `json:"douban_id"`
	VodID           string    `json:"vod_id"`
	Title           string    `json:"title"`
	Poster          string    `json:"poster"`
	Episode         string    `json:"episode"`
	SeasonNumber    int       `json:"season_number,omitempty"`
	EpisodeKey      string    `json:"episode_key,omitempty"`
	Progress        int       `json:"progress"`
	LastTime        float64   `json:"last_time"`
	Duration        float64   `json:"duration"`
	Source          string    `json:"source"`
	PreferredSource string    `json:"preferred_source_key,omitempty"`
	PreferredVodID  string    `json:"preferred_vod_id,omitempty"`
	EntryPage       string    `json:"entry_page"`
	WatchedAt       time.Time `json:"watched_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
