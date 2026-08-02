package restic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/depado/buoy/internal/types"
)

type message struct {
	MessageType string `json:"message_type"`
}

// ParseBackupStream reads restic's JSON line output from stdout.
// It calls onError for each file-level error.
// Returns the final backup summary and any fatal exit error.
func parseBackupStream(stdout io.Reader, onError func(BackupError)) (*BackupSummary, *ExitError, error) {
	var summary *BackupSummary
	var exitErr *ExitError

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.MessageType {
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

// ParseStats decodes the JSON output from "restic stats --json".
// restic writes a human-readable progress line before the JSON object,
// so we scan for the line starting with '{'.
func parseStats(r io.Reader) (*types.Stats, error) {
	var stats types.Stats
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if err := json.Unmarshal(line, &stats); err != nil {
			return nil, fmt.Errorf("parse stats: %w", err)
		}
		return &stats, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse stats: %w", err)
	}
	return nil, fmt.Errorf("parse stats: no JSON line found")
}
