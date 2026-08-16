package doubanpopular

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type Subject struct {
	ID           string
	Title        string
	Rate         string
	Cover        string
	URL          string
	EpisodesInfo string
}

type item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Pic   struct {
		Normal string `json:"normal"`
		Large  string `json:"large"`
	} `json:"pic"`
	Rating struct {
		Value float64 `json:"value"`
	} `json:"rating"`
	EpisodesInfo string `json:"episodes_info"`
}

func FetchRexxar(ctx context.Context, client *http.Client, mediaType string) ([]Subject, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoints, collection, err := rexxarEndpoints(mediaType)
	if err != nil {
		return nil, err
	}
	items := make([]item, 0, 50)
	var lastErr error
	for _, endpoint := range endpoints {
		fetched, fetchErr := fetch(ctx, client, endpoint, collection)
		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}
		items = append(items, fetched...)
	}
	if len(items) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("Rexxar returned no subjects")
		}
		return nil, lastErr
	}
	result := make([]Subject, 0, len(items))
	for _, value := range items {
		cover := value.Pic.Normal
		if cover == "" {
			cover = value.Pic.Large
		}
		result = append(result, Subject{
			ID: value.ID, Title: value.Title, Rate: fmt.Sprintf("%.1f", value.Rating.Value),
			Cover: cover, URL: "https://movie.douban.com/subject/" + url.PathEscape(value.ID) + "/", EpisodesInfo: value.EpisodesInfo,
		})
	}
	return result, nil
}

func rexxarEndpoints(mediaType string) ([]string, bool, error) {
	const base = "https://m.douban.com/rexxar/api/v2/"
	switch mediaType {
	case "movie":
		return []string{base + "movie/hot_gaia?area=%E5%85%A8%E9%83%A8&sort=recommend&playable=0&loc_id=0&start=0&count=50&for_mobile=1"}, false, nil
	case "tv":
		return []string{
			base + "subject_collection/tv_domestic/items?items_only=1&start=0&count=25&for_mobile=1",
			base + "subject_collection/tv_american/items?items_only=1&start=0&count=25&for_mobile=1",
		}, true, nil
	case "show":
		return []string{base + "subject_collection/tv_variety_show/items?items_only=1&start=0&count=50&for_mobile=1"}, true, nil
	case "cartoon":
		return []string{base + "subject_collection/tv_animation/items?items_only=1&start=0&count=50&for_mobile=1"}, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported media type %q", mediaType)
	}
}

func fetch(ctx context.Context, client *http.Client, endpoint string, collection bool) ([]item, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
	request.Header.Set("Referer", "https://m.douban.com/")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cookie", "bid="+randomBid())
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Rexxar returned HTTP %d", response.StatusCode)
	}
	if collection {
		var payload struct {
			Items []item `json:"subject_collection_items"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return nil, fmt.Errorf("decode Rexxar collection: %w", err)
		}
		return payload.Items, nil
	}
	var payload struct {
		Items []item `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Rexxar movie list: %w", err)
	}
	return payload.Items, nil
}

func randomBid() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	buffer := make([]byte, 11)
	if _, err := rand.Read(buffer); err != nil {
		return "MoovieGuest"
	}
	for index := range buffer {
		buffer[index] = alphabet[int(buffer[index])%len(alphabet)]
	}
	return string(buffer)
}
