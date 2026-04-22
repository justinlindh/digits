package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Stats struct {
	TotalUsers      int `json:"total_users"`
	TotalHouseholds int `json:"total_households"`
	TotalPhones     int `json:"total_phones"`
	OnlinePhones    int `json:"online_phones"`
	ActiveCalls     int `json:"active_calls"`
	TotalLinks      int `json:"total_links"`
}

type StatsClient struct {
	url    string
	secret string
	client *http.Client
}

func NewStatsClient(url, secret string) *StatsClient {
	return &StatsClient{
		url:    url,
		secret: secret,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *StatsClient) Fetch(ctx context.Context) (*Stats, error) {
	req, err := http.NewRequest("GET", c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Admin-Secret", c.secret)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch stats: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stats API returned %d", resp.StatusCode)
	}

	var stats Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}
	return &stats, nil
}
