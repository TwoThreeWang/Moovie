package danmaku

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxUpstreamBody = 24 << 20

type upstreamClient struct {
	httpClient *http.Client
	base       string
}

type matchResponse struct {
	IsMatched bool `json:"isMatched"`
	Matches   []struct {
		EpisodeID int64 `json:"episodeId"`
	} `json:"matches"`
}

type upstreamComment struct {
	Parameters string `json:"p"`
	Message    string `json:"m"`
}

type commentResponse struct {
	Comments []upstreamComment `json:"comments"`
}

func newUpstreamClient(httpClient *http.Client, base string) *upstreamClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &upstreamClient{httpClient: httpClient, base: strings.TrimRight(base, "/")}
}

func (client *upstreamClient) fetch(ctx context.Context, title string, season, episode int) ([]Item, error) {
	filename := title
	if episode > 0 {
		filename = fmt.Sprintf("%s S%02dE%02d", title, season, episode)
	}
	payload, _ := json.Marshal(map[string]string{"fileName": filename})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.base+"/api/v2/match", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("match returned %d", response.StatusCode)
	}
	var match matchResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&match); err != nil {
		return nil, err
	}
	if !match.IsMatched || len(match.Matches) == 0 || match.Matches[0].EpisodeID == 0 {
		return []Item{}, nil
	}

	commentURL := client.base + "/api/v2/comment/" + strconv.FormatInt(match.Matches[0].EpisodeID, 10) + "?format=json"
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, commentURL, nil)
	if err != nil {
		return nil, err
	}
	response, err = client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return []Item{}, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("comment returned %d", response.StatusCode)
	}
	var comments commentResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxUpstreamBody)).Decode(&comments); err != nil {
		return nil, err
	}
	return convertComments(comments.Comments), nil
}

func convertComments(comments []upstreamComment) []Item {
	items := make([]Item, 0, len(comments))
	for _, comment := range comments {
		text := strings.TrimSpace(comment.Message)
		parts := strings.Split(comment.Parameters, ",")
		if text == "" || len(parts) < 3 {
			continue
		}
		seconds, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil || seconds < 0 {
			continue
		}
		mode := 0
		switch strings.TrimSpace(parts[1]) {
		case "4":
			mode = 2
		case "5":
			mode = 1
		}
		color := "#FFFFFF"
		if value, parseErr := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); parseErr == nil && value >= 0 && value <= 0xFFFFFF {
			color = fmt.Sprintf("#%06X", value)
		}
		items = append(items, Item{Text: text, Time: seconds, Mode: mode, Color: color})
	}
	return items
}

func sample(items []Item, maximum int) []Item {
	if maximum <= 0 || len(items) <= maximum {
		return items
	}
	result := make([]Item, 0, maximum)
	step := float64(len(items)) / float64(maximum)
	for index := 0; index < maximum; index++ {
		position := int(float64(index) * step)
		if position >= len(items) {
			break
		}
		result = append(result, items[position])
	}
	return result
}
