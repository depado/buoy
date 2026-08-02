package types

import (
	"strconv"
	"strings"
	"time"
)

type RepoEntry struct {
	URL            string    `json:"url"`
	RepoName       string    `json:"repo_name,omitempty"`
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

type RepoRef struct {
	Name string
	URL  string
}

type RetentionPolicy struct {
	KeepLast    int
	KeepHourly  int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	KeepWithin  string
}

// IsZero reports whether the policy has no retention rules set.
func (rp RetentionPolicy) IsZero() bool {
	return rp.KeepLast == 0 &&
		rp.KeepHourly == 0 &&
		rp.KeepDaily == 0 &&
		rp.KeepWeekly == 0 &&
		rp.KeepMonthly == 0 &&
		rp.KeepYearly == 0 &&
		rp.KeepWithin == ""
}

func ParseRetentionPolicy(s string) RetentionPolicy {
	var rp RetentionPolicy
	if s == "" {
		return rp
	}
	for _, part := range SplitTrim(s) {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "keep-last":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepLast = n
			}
		case "keep-hourly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepHourly = n
			}
		case "keep-daily":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepDaily = n
			}
		case "keep-weekly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepWeekly = n
			}
		case "keep-monthly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepMonthly = n
			}
		case "keep-yearly":
			if n, err := strconv.Atoi(val); err == nil {
				rp.KeepYearly = n
			}
		case "keep-within":
			rp.KeepWithin = val
		}
	}
	return rp
}

func SplitTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
