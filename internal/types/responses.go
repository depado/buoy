package types

import "time"

// OrphanedFilter controls whether to include orphaned repos in API calls.
type OrphanedFilter string

const (
	AllRepos    OrphanedFilter = ""
	Orphaned    OrphanedFilter = "true"
	NonOrphaned OrphanedFilter = "false"
)

// RepoResult is the outcome of a per-repo operation (check, unlock, forget, prune).
type RepoResult struct {
	Repo  string `json:"repo"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// StatsResponse is the envelope for repo statistics.
type StatsResponse struct {
	Total *Stats      `json:"total"`
	Repos []RepoStats `json:"repos"`
}

// RepoStats holds statistics for a single repository.
type RepoStats struct {
	Repo  string `json:"repo"`
	Stats *Stats `json:"stats,omitempty"`
	Error string `json:"error,omitempty"`
}

// ScheduledResponse is the envelope for the scheduled containers list.
type ScheduledResponse struct {
	ContainerID    string          `json:"container_id"`
	ContainerName  string          `json:"container_name"`
	ComposeProject string          `json:"compose_project,omitempty"`
	ComposeService string          `json:"compose_service,omitempty"`
	Schedule       string          `json:"schedule"`
	Repos          []ScheduledRepo `json:"repos,omitempty"`
	StopBefore     bool            `json:"stop_before"`
}

// ScheduledRepo holds per-repo information inside a ScheduledResponse.
type ScheduledRepo struct {
	URL          string    `json:"url"`
	RepoName     string    `json:"repo_name,omitempty"`
	Created      bool      `json:"created"`
	LastBackupAt time.Time `json:"last_backup_at"`
	LastBackupOK bool      `json:"last_backup_ok"`
}

// BackupResult is the outcome of a triggered backup operation.
type BackupResult struct {
	Container string `json:"container"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}
