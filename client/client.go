package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/depado/buoy/internal/types"
)

const defaultTimeout = 5 * time.Minute

type OrphanedFilter string

const (
	AllRepos    OrphanedFilter = ""
	Orphaned    OrphanedFilter = "true"
	NonOrphaned OrphanedFilter = "false"
)

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

func (c *Client) ListRepos(repo string, orphaned OrphanedFilter) ([]types.RepoEntry, error) {
	params := make(url.Values)
	addQuery(params, repo, orphaned)
	path := "/api/v1/repos" + queryString(params)

	resp, err := c.doRequest("GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]types.RepoEntry](resp)
}

func (c *Client) ListScheduled() ([]ScheduledEntry, error) {
	resp, err := c.doRequest("GET", "/api/v1/scheduled")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]ScheduledEntry](resp)
}

func (c *Client) CheckRepos(repo string, readData bool, orphaned OrphanedFilter) ([]Result, error) {
	params := make(url.Values)
	addQuery(params, repo, orphaned)
	if readData {
		params.Set("read-data", "true")
	}
	path := "/api/v1/repos/check" + queryString(params)

	resp, err := c.doRequest("GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]Result](resp)
}

func (c *Client) StatsRepos(repo string, orphaned OrphanedFilter) (*StatsResponse, error) {
	params := make(url.Values)
	addQuery(params, repo, orphaned)
	path := "/api/v1/repos/stats" + queryString(params)

	resp, err := c.doRequest("GET", path)
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

func (c *Client) UnlockRepos(repo string, orphaned OrphanedFilter) ([]Result, error) {
	params := make(url.Values)
	addQuery(params, repo, orphaned)
	path := "/api/v1/repos/unlock" + queryString(params)

	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]Result](resp)
}

func (c *Client) ForgetRepos(repo string, retention string, orphaned OrphanedFilter) ([]Result, error) {
	params := make(url.Values)
	params.Set("retention", retention)
	addQuery(params, repo, orphaned)
	path := "/api/v1/repos/forget" + queryString(params)

	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]Result](resp)
}

func (c *Client) PruneRepos(repo string, orphaned OrphanedFilter) ([]Result, error) {
	params := make(url.Values)
	addQuery(params, repo, orphaned)
	path := "/api/v1/repos/prune" + queryString(params)

	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]Result](resp)
}

func (c *Client) TriggerBackup(containers []string) ([]BackupResult, error) {
	params := make(url.Values)
	for _, name := range containers {
		params.Add("container", name)
	}
	path := "/api/v1/backup" + queryString(params)

	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]BackupResult](resp)
}

func (c *Client) TriggerProjectBackup(project string, services []string) (*BackupResult, error) {
	params := url.Values{"project": {project}}
	for _, svc := range services {
		params.Add("container", svc)
	}
	path := "/api/v1/backup" + queryString(params)

	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	results, err := decodeJSON[[]BackupResult](resp)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	return &results[0], nil
}

func (c *Client) TriggerBackupAll() ([]BackupResult, error) {
	resp, err := c.doRequest("POST", "/api/v1/backup?all=true")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]BackupResult](resp)
}

func decodeJSON[T any](resp *http.Response) (T, error) {
	var v T
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return v, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return v, fmt.Errorf("%s", errResp.Error)
		}
		return v, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("decode response: %w", err)
	}
	return v, nil
}

func addQuery(params url.Values, repo string, orphaned OrphanedFilter) {
	if repo != "" {
		params.Set("repo", repo)
	}
	if orphaned != "" {
		params.Set("orphaned", string(orphaned))
	}
}

func queryString(params url.Values) string {
	if q := params.Encode(); q != "" {
		return "?" + q
	}
	return ""
}
