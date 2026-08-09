package restic

import (
	"strings"
	"testing"
)

func TestParseStats(t *testing.T) {
	input := `{"total_size":1024,"total_file_count":10,"snapshots_count":3}`
	stats, err := parseStats(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalSize != 1024 {
		t.Errorf("expected TotalSize 1024, got %d", stats.TotalSize)
	}
	if stats.TotalFileCount != 10 {
		t.Errorf("expected TotalFileCount 10, got %d", stats.TotalFileCount)
	}
	if stats.SnapshotsCount != 3 {
		t.Errorf("expected SnapshotsCount 3, got %d", stats.SnapshotsCount)
	}
}

func TestParseStream_Summary(t *testing.T) {
	lines := `{"message_type":"summary","files_new":5,"files_changed":0,"files_unmodified":0,"dirs_new":1,"dirs_changed":0,"dirs_unmodified":0,"data_blobs":1,"tree_blobs":1,"data_added":1024,"total_files_processed":5,"total_bytes_processed":2048,"total_duration":1.5,"snapshot_id":"snap1"}
`
	stdout := strings.NewReader(lines)
	summary, exitErr, err := parseBackupStream(stdout, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitErr != nil {
		t.Fatalf("unexpected exit error: %v", exitErr)
	}
	if summary == nil {
		t.Fatal("expected summary, got nil")
	}
	if summary.FilesNew != 5 {
		t.Errorf("expected FilesNew 5, got %d", summary.FilesNew)
	}
	if summary.SnapshotID != "snap1" {
		t.Errorf("expected SnapshotID 'snap1', got %q", summary.SnapshotID)
	}
}

func TestParseStream_ExitError(t *testing.T) {
	lines := `{"message_type":"exit_error","code":1,"message":"repository does not exist"}
`
	stdout := strings.NewReader(lines)
	_, exitErr, err := parseBackupStream(stdout, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitErr == nil {
		t.Fatal("expected exit error, got nil")
	}
	if exitErr.Code != 1 {
		t.Errorf("expected code 1, got %d", exitErr.Code)
	}
}

func TestParseStream_EmptyInput(t *testing.T) {
	stdout := strings.NewReader("")
	summary, exitErr, err := parseBackupStream(stdout, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary != nil {
		t.Error("expected nil summary for empty input")
	}
	if exitErr != nil {
		t.Error("expected nil exit error for empty input")
	}
}

func TestParseStream_LastStatus(t *testing.T) {
	lines := `{"message_type":"status","percent_done":0.5,"total_files":2,"files_done":1,"total_bytes":1024,"bytes_done":512}
{"message_type":"status","percent_done":0.83,"total_files":9312,"files_done":8422,"total_bytes":4404019200,"bytes_done":3758096384,"seconds_elapsed":3480,"seconds_remaining":712,"current_files":["/data/db.sqlite"]}
{"message_type":"summary","files_new":0}
`
	var last *BackupStatus
	_, _, err := parseBackupStream(strings.NewReader(lines), nil, func(s *BackupStatus) { last = s })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if last == nil {
		t.Fatal("expected last status, got nil")
	}
	want := `83.0%, 3.5GB/4.1GB, 8422/9312 files, 3480s elapsed, 712s left, processing "/data/db.sqlite"`
	if got := last.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
