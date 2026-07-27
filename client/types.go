package client

import (
	"time"

	"github.com/depado/buoy/internal/types"
)

type Result struct {
	Repo  string `json:"repo"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type StatsResponse struct {
	Total *types.Stats `json:"total"`
	Repos []RepoStats  `json:"repos"`
}

type RepoStats struct {
	Repo  string       `json:"repo"`
	Stats *types.Stats `json:"stats,omitempty"`
	Error string       `json:"error,omitempty"`
}

type ScheduledRepo struct {
	URL          string    `json:"url"`
	RepoName     string    `json:"repo_name,omitempty"`
	Created      bool      `json:"created"`
	LastBackupAt time.Time `json:"last_backup_at"`
	LastBackupOK bool      `json:"last_backup_ok"`
}

type ScheduledEntry struct {
	ContainerID    string          `json:"container_id"`
	ContainerName  string          `json:"container_name"`
	ComposeProject string          `json:"compose_project,omitempty"`
	ComposeService string          `json:"compose_service,omitempty"`
	Schedule       string          `json:"schedule"`
	Repos          []ScheduledRepo `json:"repos,omitempty"`
	StopBefore     bool            `json:"stop_before"`
}

type BackupResult struct {
	Container string `json:"container"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}
