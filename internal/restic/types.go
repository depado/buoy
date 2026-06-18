package restic

// BackupStatus represents a progress update from restic during a backup.
type BackupStatus struct {
	SecondsElapsed   uint64   `json:"seconds_elapsed"`
	SecondsRemaining uint64   `json:"seconds_remaining"`
	PercentDone      float64  `json:"percent_done"`
	TotalFiles       uint64   `json:"total_files"`
	FilesDone        uint64   `json:"files_done"`
	TotalBytes       uint64   `json:"total_bytes"`
	BytesDone        uint64   `json:"bytes_done"`
	ErrorCount       uint64   `json:"error_count"`
	CurrentFiles     []string `json:"current_files"`
}

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

// InitResult is returned when a restic repository is successfully initialized.
type InitResult struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
}

// Snapshot represents a restic snapshot.
type Snapshot struct {
	ID       string           `json:"id"`
	ShortID  string           `json:"short_id"`
	Time     string           `json:"time"`
	Parent   string           `json:"parent"`
	Tree     string           `json:"tree"`
	Paths    []string         `json:"paths"`
	Hostname string           `json:"hostname"`
	Username string           `json:"username"`
	UID      uint32           `json:"uid"`
	GID      uint32           `json:"gid"`
	Tags     []string         `json:"tags"`
	Summary  *SnapshotSummary `json:"summary"`
}

// SnapshotSummary contains statistics about a snapshot.
type SnapshotSummary struct {
	FilesNew        uint64  `json:"files_new"`
	FilesChanged    uint64  `json:"files_changed"`
	FilesUnmodified uint64  `json:"files_unmodified"`
	DirsNew         uint64  `json:"dirs_new"`
	DirsChanged     uint64  `json:"dirs_changed"`
	DirsUnmodified  uint64  `json:"dirs_unmodified"`
	DataAdded       uint64  `json:"data_added"`
	TotalDuration   float64 `json:"total_duration"`
}

// Stats contains repository-level statistics.
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

// BackupOptions configures a restic backup operation.
type BackupOptions struct {
	Tags     []string
	Excludes []string
	Files    []string
	Hostname string
	WorkDir  string
}

// RetentionPolicy defines how many snapshots to keep for each time period.
type RetentionPolicy struct {
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	KeepWithin  string
}
