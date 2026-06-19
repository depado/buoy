package restic

import (
	"strings"
	"testing"
)

func TestParseSnapshots(t *testing.T) {
	input := `[{"id":"abc","short_id":"abc123","time":"2026-01-01T00:00:00Z"}]`
	snapshots, err := ParseSnapshots(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	if snapshots[0].ID != "abc" {
		t.Errorf("expected ID 'abc', got %q", snapshots[0].ID)
	}
	if snapshots[0].ShortID != "abc123" {
		t.Errorf("expected ShortID 'abc123', got %q", snapshots[0].ShortID)
	}
}

func TestParseSnapshots_Empty(t *testing.T) {
	input := `[]`
	snapshots, err := ParseSnapshots(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snapshots))
	}
}

func TestParseSnapshots_InvalidJSON(t *testing.T) {
	_, err := ParseSnapshots(strings.NewReader("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseStats(t *testing.T) {
	input := `{"total_size":1024,"total_file_count":10,"snapshots_count":3}`
	stats, err := ParseStats(strings.NewReader(input))
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

func TestParseBackupStream_Summary(t *testing.T) {
	lines := `{"message_type":"summary","files_new":5,"files_changed":0,"files_unmodified":0,"dirs_new":1,"dirs_changed":0,"dirs_unmodified":0,"data_blobs":1,"tree_blobs":1,"data_added":1024,"total_files_processed":5,"total_bytes_processed":2048,"total_duration":1.5,"snapshot_id":"snap1"}
`
	stdout := strings.NewReader(lines)
	summary, exitErr, err := ParseBackupStream(stdout, nil, nil)
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

func TestParseBackupStream_StatusCallback(t *testing.T) {
	lines := `{"message_type":"status","percent_done":50.0,"files_done":10,"total_files":20}
{"message_type":"status","percent_done":100.0,"files_done":20,"total_files":20}
`
	var statuses []BackupStatus
	onStatus := func(s BackupStatus) {
		statuses = append(statuses, s)
	}
	stdout := strings.NewReader(lines)
	_, _, err := ParseBackupStream(stdout, onStatus, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 status callbacks, got %d", len(statuses))
	}
	if statuses[0].PercentDone != 50.0 {
		t.Errorf("expected 50.0%%, got %f", statuses[0].PercentDone)
	}
}

func TestParseBackupStream_ExitError(t *testing.T) {
	lines := `{"message_type":"exit_error","code":1,"message":"repository does not exist"}
`
	stdout := strings.NewReader(lines)
	_, exitErr, err := ParseBackupStream(stdout, nil, nil)
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

func TestParseBackupStream_EmptyInput(t *testing.T) {
	stdout := strings.NewReader("")
	summary, exitErr, err := ParseBackupStream(stdout, nil, nil)
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
