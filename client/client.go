package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
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

func (c *Client) ListRepos(repo string, orphaned bool) ([]RepoEntry, error) {
	path := "/api/v1/repos" + listQuery(repo, orphaned)
	resp, err := c.doRequest("GET", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]RepoEntry](resp)
}

func (c *Client) ListScheduled() ([]ScheduledEntry, error) {
	resp, err := c.doRequest("GET", "/api/v1/scheduled")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]ScheduledEntry](resp)
}

func (c *Client) CheckRepos(repo string, readData bool, orphaned bool) ([]Result, error) {
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
	return decodeJSON[[]Result](resp)
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

func (c *Client) UnlockRepos(repo string, orphaned bool) ([]Result, error) {
	path := "/api/v1/repos/unlock" + listQuery(repo, orphaned)
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]Result](resp)
}

func (c *Client) ForgetRepos(repo string, retention string, orphaned bool) ([]Result, error) {
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
	return decodeJSON[[]Result](resp)
}

func (c *Client) PruneRepos(repo string, orphaned bool) ([]Result, error) {
	path := "/api/v1/repos/prune" + listQuery(repo, orphaned)
	resp, err := c.doRequest("POST", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeJSON[[]Result](resp)
}

func decodeJSON[T any](resp *http.Response) (T, error) {
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return v, fmt.Errorf("decode response: %w", err)
	}
	return v, nil
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
