package client

import (
	"time"
)

type RepoEntry struct {
	URL            string    `json:"url"`
	ContainerID    string    `json:"container_id"`
	ContainerName  string    `json:"container_name"`
	ComposeProject string    `json:"compose_project,omitempty"`
	ComposeService string    `json:"compose_service,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastBackupAt   time.Time `json:"last_backup_at"`
	LastBackupOK   bool      `json:"last_backup_ok"`
	LastCheckAt    time.Time `json:"last_check_at"`
	LastCheckOK    bool      `json:"last_check_ok"`
	Orphaned       bool      `json:"orphaned"`
}

// Result is the result of an operation on a repository (check, unlock, forget, prune).
type Result struct {
	Repo  string `json:"repo"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type Stats struct {
	TotalSize              uint64  `json:"total_size"`
	TotalFileCount         uint64  `json:"total_file_count"`
	TotalBlobCount         uint64  `json:"total_blob_count"`
	SnapshotsCount         uint64  `json:"snapshots_count"`
	TotalUncompressedSize  uint64  `json:"total_uncompressed_size"`
	CompressionRatio       float64 `json:"compression_ratio"`
	CompressionProgress    float64 `json:"compression_progress"`
	CompressionSpaceSaving float64 `json:"compression_space_saving"`
}

type StatsResponse struct {
	Total *Stats      `json:"total"`
	Repos []RepoStats `json:"repos"`
}

type RepoStats struct {
	Repo  string `json:"repo"`
	Stats *Stats `json:"stats,omitempty"`
	Error string `json:"error,omitempty"`
}
