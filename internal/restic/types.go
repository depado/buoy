package restic

import (
	"fmt"
	"strings"
)

// BackupSummary is the final result of a successful restic backup.
type BackupSummary struct {
	FilesNew            uint64  `json:"files_new"`
	FilesChanged        uint64  `json:"files_changed"`
	FilesUnmodified     uint64  `json:"files_unmodified"`
	DirsNew             uint64  `json:"dirs_new"`
	DirsChanged         uint64  `json:"dirs_changed"`
	DirsUnmodified      uint64  `json:"dirs_unmodified"`
	DataBlobs           int64   `json:"data_blobs"`
	TreeBlobs           int64   `json:"tree_blobs"`
	DataAdded           uint64  `json:"data_added"`
	TotalFilesProcessed uint64  `json:"total_files_processed"`
	TotalBytesProcessed uint64  `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
	SnapshotID          string  `json:"snapshot_id"`
}

// BackupStatus is a progress line restic emits during backup (status message).
// The last one received is kept so an interrupted backup can report where it
// was when it stopped.
type BackupStatus struct {
	PercentDone      float64  `json:"percent_done"`
	TotalFiles       uint64   `json:"total_files"`
	FilesDone        uint64   `json:"files_done"`
	TotalBytes       uint64   `json:"total_bytes"`
	BytesDone        uint64   `json:"bytes_done"`
	CurrentFiles     []string `json:"current_files"`
	SecondsElapsed   uint64   `json:"seconds_elapsed"`
	SecondsRemaining uint64   `json:"seconds_remaining"`
}

// String renders the status line for inclusion in error messages.
func (s *BackupStatus) String() string {
	var parts []string
	if s.PercentDone > 0 {
		parts = append(parts, fmt.Sprintf("%.1f%%", s.PercentDone*100))
	}
	if s.TotalBytes > 0 {
		parts = append(parts, fmt.Sprintf("%s/%s", formatBytes(s.BytesDone), formatBytes(s.TotalBytes)))
	}
	if s.TotalFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d files", s.FilesDone, s.TotalFiles))
	}
	if s.SecondsElapsed > 0 {
		parts = append(parts, fmt.Sprintf("%ds elapsed", s.SecondsElapsed))
	}
	if s.SecondsRemaining > 0 {
		parts = append(parts, fmt.Sprintf("%ds left", s.SecondsRemaining))
	}
	if len(s.CurrentFiles) > 0 {
		parts = append(parts, fmt.Sprintf("processing %q", s.CurrentFiles[0]))
	}
	return strings.Join(parts, ", ")
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// BackupError represents a file-level error encountered during backup.
type BackupError struct {
	Message string `json:"message"`
	During  string `json:"during"`
	Item    string `json:"item"`
}

// ExitError is a fatal error that causes restic to exit with a non-zero code.
type ExitError struct {
	MessageType string `json:"message_type"`
	Code        int    `json:"code"`
	Message     string `json:"message"`
}

// BackupOptions configures a restic backup operation.
type BackupOptions struct {
	Tags     []string
	Excludes []string
	Files    []string
	Hostname string
	WorkDir  string
}
