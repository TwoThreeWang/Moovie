package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/user/moovie/internal/config"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"golang.org/x/sync/singleflight"
)

type TMDBService struct {
	movieRepo *repository.MovieRepository
	config    *config.Config
	group     singleflight.Group
}

func NewTMDBService(repo *repository.MovieRepository, cfg *config.Config) *TMDBService {
	return &TMDBService{
		movieRepo: repo,
		config:    cfg,
	}
}

// FetchAndSync 从 TMDB 同步数据并保存到数据库
func (s *TMDBService) FetchAndSync(doubanID string) (*model.Movie, error) {
	// 使用 singleflight 避免并发重复抓取
	val, err, _ := s.group.Do(doubanID, func() (interface{}, error) {
		return s.fetchAndSyncInternal(doubanID)
	})
	if err != nil {
		return nil, err
	}
	return val.(*model.Movie), nil
}

func (s *TMDBService) fetchAndSyncInternal(doubanID string) (*model.Movie, error) {
	movie, err := s.movieRepo.FindByDoubanID(doubanID)
	if err != nil {
		return nil, err
	}
	if movie == nil {
		return nil, fmt.Errorf("movie not found in database: %s", doubanID)
	}

	// 1. 获取 IMDb ID (如果缺失)
	if movie.IMDbID == "" {
		imdbID, err := s.fetchIMDbIDFromWMDB(doubanID)
		if err != nil {
			log.Printf("[TMDB] 从 WMDB 获取 IMDb ID 失败 (DoubanID: %s): %v", doubanID, err)
		} else if imdbID != "" {
			movie.IMDbID = imdbID
		}
	}

	if movie.IMDbID == "" {
		return movie, fmt.Errorf("IMDb ID not found for DoubanID: %s", doubanID)
	}

	// 2. 通过 IMDb ID 获取 TMDB ID
	tmdbID, mediaType, err := s.findTMDBID(movie.IMDbID)
	if err != nil {
		return movie, fmt.Errorf("failed to find TMDB ID: %w", err)
	}

	// 3. 获取 TMDB 剧照
	tmdbImages, err := s.fetchTMDBImages(tmdbID, mediaType)
	if err != nil {
		log.Printf("[TMDB] 获取剧照失败: %v", err)
	}

	// 4. 获取 TMDB 详情 (用于补全缺失字段)
	tmdbDetails, err := s.fetchTMDBDetails(tmdbID, mediaType)
	if err != nil {
		log.Printf("[TMDB] 获取详情失败: %v", err)
	}

	// 5. 更新电影信息
	s.applyTMDBData(movie, tmdbImages, tmdbDetails)

	// 6. 保存到数据库
	if err := s.movieRepo.Upsert(movie); err != nil {
		return movie, fmt.Errorf("failed to update movie: %w", err)
	}

	return movie, nil
}

func (s *TMDBService) fetchIMDbIDFromWMDB(doubanID string) (string, error) {
	url := fmt.Sprintf("https://api.wmdb.tv/movie/api?id=%s", doubanID)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		IMDbID string `json:"imdbId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.IMDbID, nil
}

type tmdbFindResponse struct {
	MovieResults []struct {
		ID int `json:"id"`
	} `json:"movie_results"`
	TVResults []struct {
		ID int `json:"id"`
	} `json:"tv_results"`
}

func (s *TMDBService) findTMDBID(imdbID string) (int, string, error) {
	url := fmt.Sprintf("https://api.themoviedb.org/3/find/%s?external_source=imdb_id&language=zh-CN", imdbID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.config.TMDBToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	var result tmdbFindResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", err
	}

	if len(result.MovieResults) > 0 {
		return result.MovieResults[0].ID, "movie", nil
	}
	if len(result.TVResults) > 0 {
		return result.TVResults[0].ID, "tv", nil
	}

	return 0, "", fmt.Errorf("no TMDB results found for IMDb ID: %s", imdbID)
}

type tmdbImagesResponse struct {
	ID        int `json:"id"`
	Backdrops []struct {
		FilePath string `json:"file_path"`
	} `json:"backdrops"`
	Posters []struct {
		FilePath string `json:"file_path"`
	} `json:"posters"`
}

func (s *TMDBService) fetchTMDBImages(tmdbID int, mediaType string) (*tmdbImagesResponse, error) {
	url := fmt.Sprintf("https://api.themoviedb.org/3/%s/%d/images?include_image_language=zh,en,null", mediaType, tmdbID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.config.TMDBToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result tmdbImagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

type tmdbDetailsResponse struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	OriginalTitle  string `json:"original_title"`
	Overview       string `json:"overview"`
	ReleaseDate    string `json:"release_date"`
	FirstAirDate   string `json:"first_air_date"` // 电视剧
	Runtime        int    `json:"runtime"`
	EpisodeRunTime []int  `json:"episode_run_time"` // 电视剧
	Genres         []struct {
		Name string `json:"name"`
	} `json:"genres"`
}

func (s *TMDBService) fetchTMDBDetails(tmdbID int, mediaType string) (*tmdbDetailsResponse, error) {
	url := fmt.Sprintf("https://api.themoviedb.org/3/%s/%d?language=zh-CN", mediaType, tmdbID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.config.TMDBToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result tmdbDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (s *TMDBService) applyTMDBData(movie *model.Movie, images *tmdbImagesResponse, details *tmdbDetailsResponse) {
	// 1. 处理剧照 (Backdrops)
	var backdropURLs []string
	if images != nil {
		for _, b := range images.Backdrops {
			backdropURLs = append(backdropURLs, "https://image.tmdb.org/t/p/w1280"+b.FilePath)
		}
		if len(backdropURLs) > 0 {
			movie.Backdrops = strings.Join(backdropURLs, ",")
		}

		// 2. 处理海报 (Poster) - 如果有 TMDB 海报，替换豆瓣海报
		if len(images.Posters) > 0 {
			movie.Poster = "https://image.tmdb.org/t/p/w500" + images.Posters[0].FilePath
		}
	}

	// 3. 补全缺失字段
	if details != nil {
		if movie.OriginalTitle == "" {
			movie.OriginalTitle = details.OriginalTitle
		}
		if movie.Summary == "" {
			movie.Summary = details.Overview
		}
		if movie.Year == "" {
			date := details.ReleaseDate
			if date == "" {
				date = details.FirstAirDate
			}
			if len(date) >= 4 {
				movie.Year = date[:4]
			}
		}
		if movie.Duration == "" {
			if details.Runtime > 0 {
				movie.Duration = fmt.Sprintf("%d分钟", details.Runtime)
			} else if len(details.EpisodeRunTime) > 0 {
				movie.Duration = fmt.Sprintf("%d分钟", details.EpisodeRunTime[0])
			}
		}
		if movie.Genres == "" && len(details.Genres) > 0 {
			var genres []string
			for _, g := range details.Genres {
				genres = append(genres, g.Name)
			}
			movie.Genres = strings.Join(genres, "/")
		}
	}
}

// SyncMovieSafeAsync 异步安全同步 TMDB 信息
func (s *TMDBService) SyncMovieSafeAsync(doubanID string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[TMDB] 异步抓取发生恐慌 (DoubanID: %s): %v", doubanID, r)
			}
		}()

		// 增加随机延迟，避免请求过频
		time.Sleep(time.Duration(200+(time.Now().UnixNano()%800)) * time.Millisecond)

		if _, err := s.FetchAndSync(doubanID); err != nil {
			log.Printf("[TMDB] 异步抓取失败 (DoubanID: %s): %v", doubanID, err)
		}
	}()
}
