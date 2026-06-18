package restic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

type message struct {
	MessageType string `json:"message_type"`
}

// ParseBackupStream reads restic's JSON line output from stdout and stderr.
// It calls onStatus for each progress update and onError for each file-level error.
// Returns the final backup summary and any fatal exit error. stderr may be nil.
func ParseBackupStream(stdout, stderr io.Reader, onStatus func(BackupStatus), onError func(BackupError)) (*BackupSummary, *ExitError, error) {
	var summary *BackupSummary
	var exitErr *ExitError

	if stderr != nil {
		go parseStderr(stderr, onError)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.MessageType {
		case "status":
			var s BackupStatus
			if err := json.Unmarshal(line, &s); err == nil && onStatus != nil {
				onStatus(s)
			}
		case "summary":
			var s BackupSummary
			if err := json.Unmarshal(line, &s); err == nil {
				summary = &s
			}
		case "error":
			var e BackupError
			if err := json.Unmarshal(line, &e); err == nil && onError != nil {
				onError(e)
			}
		case "exit_error":
			var e ExitError
			if err := json.Unmarshal(line, &e); err == nil {
				exitErr = &e
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return summary, exitErr, fmt.Errorf("scan restic output: %w", err)
	}
	return summary, exitErr, nil
}

func parseStderr(r io.Reader, onError func(BackupError)) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.MessageType == "error" {
			var e BackupError
			if err := json.Unmarshal(line, &e); err == nil && onError != nil {
				onError(e)
			}
		}
	}
}

// ParseSnapshots decodes the JSON array output from "restic snapshots --json".
func ParseSnapshots(r io.Reader) ([]Snapshot, error) {
	var snapshots []Snapshot
	if err := json.NewDecoder(r).Decode(&snapshots); err != nil {
		return nil, fmt.Errorf("parse snapshots: %w", err)
	}
	return snapshots, nil
}

// ParseStats decodes the JSON output from "restic stats --json".
func ParseStats(r io.Reader) (*Stats, error) {
	var stats Stats
	if err := json.NewDecoder(r).Decode(&stats); err != nil {
		return nil, fmt.Errorf("parse stats: %w", err)
	}
	return &stats, nil
}

// ParseInit decodes the JSON output from "restic init --json".
func ParseInit(r io.Reader) (*InitResult, error) {
	var result InitResult
	if err := json.NewDecoder(r).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse init: %w", err)
	}
	return &result, nil
}
