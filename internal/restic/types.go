package restic

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
