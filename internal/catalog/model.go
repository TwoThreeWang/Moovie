// Package catalog 负责影视资料：从豆瓣/TMDB 抓取元数据、生成语义向量、
// 渲染影片详情页和发现页，并把资料同步进规范媒体表 media。
//
// 主要涉及的表：
//
//	media                规范媒体主表（Movie 结构就是它的页面视图）
//	media_external_ids   外部 ID 映射（imdb / tmdb / douban）
//	media_aliases        别名        media_units 季集信息
//	worker_jobs          资料刷新任务队列
//
// 数据来源分工：豆瓣给主资料和短评，TMDB 给剧照和季集，Wikidata/wmdb 给 IMDb 映射，
// Ollama 给向量（可选再经 AI Gateway 改写文案）。
package catalog

import "time"

// Movie 是 media 表在页面层的视图结构。
// 注意它并不等于数据库字段：IMDbID 来自 media_external_ids，Embedding 来自 pgvector 列。
type Movie struct {
	ID                    int
	DoubanID              string
	Title                 string
	OriginalTitle         string
	Year                  string
	Poster                string
	Rating                float64
	Genres                string
	Countries             string
	Directors             string
	Actors                string
	Summary               string
	Duration              string
	IMDbID                string
	MediaType             string
	SeriesStatus          string
	Backdrops             string
	EmbeddingContent      string
	EmbeddingSemanticHash string
	Embedding             []float32
	ReviewsJSON           string
	ReviewsUpdatedAt      time.Time
	MetadataStatus        string
	CompletenessScore     int
	NextRefreshAt         *time.Time
	UpdatedAt             time.Time
}

// SeriesSeason 是详情页季度导航所需的最小数据。
type SeriesSeason struct {
	DoubanID     string
	Title        string
	Year         string
	Rating       float64
	SeasonNumber int
	Current      bool
}

// Director 同时用于导演和演员（两者都以 JSON 数组存在 media.directors / media.actors 里）。
type Director struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
