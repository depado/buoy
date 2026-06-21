package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
)

const defaultTimeout = 5 * time.Minute

type Client struct {
	BaseURL string
	Token   string
	client  *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		client:  &http.Client{Timeout: defaultTimeout},
	}
}

func DefaultClient() *Client {
	return NewClient(
		envOrDefault("BUOY_URL", "http://127.0.0.1:8080"),
		os.Getenv("BUOY_TOKEN"),
	)
}

func (c *Client) doRequest(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	return resp, nil
}

func (c *Client) ListRepos(repo string, orphaned bool) ([]registry.RepoEntry, error) {
	path := "/api/v1/repos" + listQuery(repo, orphaned)
	resp, err := c.doRequest("GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]registry.RepoEntry](resp)
}

func (c *Client) CheckRepos(repo string, readData bool, orphaned bool) ([]CheckResult, error) {
	params := make(url.Values)
	appendRepoParam(params, repo)
	if readData {
		params.Set("read-data", "true")
	}
	if orphaned {
		params.Set("orphaned", "true")
	}
	path := "/api/v1/repos/check"
	if q := params.Encode(); q != "" {
		path += "?" + q
	}
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]CheckResult](resp)
}

type CheckResult struct {
	Repo  string `json:"repo"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *Client) StatsRepos(repo string, orphaned bool) (*StatsResponse, error) {
	path := "/api/v1/repos/stats" + listQuery(repo, orphaned)
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	stats, err := decodeJSON[StatsResponse](resp)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

type StatsResponse struct {
	Total *restic.Stats `json:"total"`
	Repos []RepoStats   `json:"repos"`
}

type RepoStats struct {
	Repo  string        `json:"repo"`
	Stats *restic.Stats `json:"stats,omitempty"`
	Error string        `json:"error,omitempty"`
}

func (c *Client) UnlockRepos(repo string, orphaned bool) ([]OpResult, error) {
	path := "/api/v1/repos/unlock" + listQuery(repo, orphaned)
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]OpResult](resp)
}

type OpResult struct {
	Repo  string `json:"repo"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (c *Client) ForgetRepos(repo string, retention string, orphaned bool) ([]OpResult, error) {
	params := url.Values{}
	params.Set("retention", retention)
	appendRepoParam(params, repo)
	if orphaned {
		params.Set("orphaned", "true")
	}
	path := "/api/v1/repos/forget?" + params.Encode()
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]OpResult](resp)
}

func (c *Client) PruneRepos(repo string, orphaned bool) ([]OpResult, error) {
	path := "/api/v1/repos/prune" + listQuery(repo, orphaned)
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]OpResult](resp)
}

func decodeJSON[T any](resp *http.Response) (T, error) {
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return v, fmt.Errorf("decode response: %w", err)
	}
	return v, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func listQuery(repo string, orphaned bool) string {
	params := make(url.Values)
	appendRepoParam(params, repo)
	if orphaned {
		params.Set("orphaned", "true")
	} else {
		params.Set("orphaned", "false")
	}
	if q := params.Encode(); q != "" {
		return "?" + q
	}
	return ""
}

func appendRepoParam(params url.Values, repo string) {
	if repo != "" {
		params.Set("repo", repo)
	}
}
