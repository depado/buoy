package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Client wraps the restic binary and provides methods for all backup operations.
type Client struct {
	binPath     string
	password    string
	compression string
}

// New creates a new restic Client using the given binary path and repository password.
func New(binPath, password, compression string) *Client {
	if compression == "" {
		compression = "auto"
	}
	return &Client{
		binPath:     binPath,
		password:    password,
		compression: compression,
	}
}

// Init initializes a new restic repository at the given location.
func (c *Client) Init(ctx context.Context, repo string) error {
	return c.runSimple(ctx, "init", "init", "-r", repo)
}

// RepoExists checks whether a restic repository exists at the given location.
// Returns false with no error if the repository does not exist (exit code 10).
func (c *Client) RepoExists(ctx context.Context, repo string) (bool, error) {
	var stderr bytes.Buffer
	cmd, cleanup, err := c.command(ctx, "cat", "config", "-r", repo)
	if err != nil {
		return false, err
	}
	defer cleanup()
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 10 {
			return false, nil
		}
		return false, fmt.Errorf("restic cat config: %w\n%s", err, stderr.String())
	}
	return true, nil
}

// Unlock removes stale locks from a repository. Safe to call before every backup.
func (c *Client) Unlock(ctx context.Context, repo string) error {
	return c.runSimple(ctx, "unlock", "unlock", "-r", repo)
}

// Backup runs a restic backup of the given paths. If opts.Files is set, those
// patterns are written to a temp file and passed via --files-from, and paths
// is ignored (restic only backs up what matches the patterns).
// If opts.WorkDir is set, the restic process runs from that directory.
func (c *Client) Backup(ctx context.Context, repo string, paths []string, opts BackupOptions) (*BackupSummary, error) {
	args := []string{"backup", "-r", repo, "--json"}
	args = append(args, "--group-by", "host,tags")
	for _, tag := range opts.Tags {
		args = append(args, "--tag", tag)
	}
	for _, exclude := range opts.Excludes {
		args = append(args, "--exclude", exclude)
	}
	if opts.Hostname != "" {
		args = append(args, "--host", opts.Hostname)
	}

	var tmpFile string
	if len(opts.Files) > 0 {
		f, err := os.CreateTemp("", "buoy-files-*.txt")
		if err != nil {
			return nil, fmt.Errorf("create files temp file: %w", err)
		}
		defer os.Remove(f.Name()) //nolint:errcheck
		if _, err := f.WriteString(strings.Join(opts.Files, "\n")); err != nil {
			f.Close() //nolint:errcheck
			return nil, fmt.Errorf("write files temp file: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("close files temp file: %w", err)
		}
		tmpFile = f.Name()
		args = append(args, "--files-from", tmpFile)
	} else {
		args = append(args, paths...)
	}

	cmd, cleanup, err := c.command(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start restic backup: %w", err)
	}

	var errors []BackupError
	summary, exitErr, parseErr := ParseBackupStream(stdout, nil, func(e BackupError) {
		errors = append(errors, e)
	})

	waitErr := cmd.Wait()

	if parseErr != nil {
		return summary, parseErr
	}
	if exitErr != nil {
		return summary, fmt.Errorf("restic backup: %s", exitErr.Message)
	}
	if waitErr != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if e := tryParseExitError(stderr); e != nil {
			return summary, fmt.Errorf("restic backup: %s", e.Message)
		}
		if stderr != "" {
			return summary, fmt.Errorf("restic backup: %w\n%s", waitErr, stderr)
		}
		return summary, fmt.Errorf("restic backup: %w", waitErr)
	}

	if len(errors) > 0 {
		return summary, fmt.Errorf("restic backup: %d file-level errors (first: %s: %s)",
			len(errors), errors[0].During, errors[0].Item)
	}

	return summary, nil
}

func tryParseExitError(s string) *ExitError {
	if s == "" {
		return nil
	}
	var e ExitError
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return nil
	}
	if e.MessageType == "exit_error" && e.Code > 0 {
		return &e
	}
	return nil
}

// Forget applies a retention policy to remove old snapshots.
// Uses --group-by host,tags to match the backup command's grouping.
func (c *Client) Forget(ctx context.Context, repo string, policy RetentionPolicy, hostname string) error {
	return c.runSimple(ctx, "forget", forgetArgs(repo, policy, hostname)...)
}

func forgetArgs(repo string, policy RetentionPolicy, hostname string) []string {
	args := []string{"forget", "-r", repo, "--json", "--group-by", "host,tags"}
	if hostname != "" {
		args = append(args, "--host", hostname)
	}
	if policy.KeepLast > 0 {
		args = append(args, "--keep-last", fmt.Sprintf("%d", policy.KeepLast))
	}
	if policy.KeepHourly > 0 {
		args = append(args, "--keep-hourly", fmt.Sprintf("%d", policy.KeepHourly))
	}
	if policy.KeepDaily > 0 {
		args = append(args, "--keep-daily", fmt.Sprintf("%d", policy.KeepDaily))
	}
	if policy.KeepWeekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprintf("%d", policy.KeepWeekly))
	}
	if policy.KeepMonthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprintf("%d", policy.KeepMonthly))
	}
	if policy.KeepYearly > 0 {
		args = append(args, "--keep-yearly", fmt.Sprintf("%d", policy.KeepYearly))
	}
	if policy.KeepWithin != "" {
		args = append(args, "--keep-within", policy.KeepWithin)
	}
	return args
}

// Prune removes unreferenced data from the repository after forget.
func (c *Client) Prune(ctx context.Context, repo string) error {
	return c.runSimple(ctx, "prune", "prune", "-r", repo)
}

// Check verifies the integrity of a restic repository.
// Uses restic check with no additional flags (structure check only).
// For data integrity verification, use CheckReadData.
func (c *Client) Check(ctx context.Context, repo string) error {
	return c.runSimple(ctx, "check", "check", "-r", repo)
}

// CheckReadData verifies the integrity of a restic repository including
// reading all pack files. This is I/O intensive but provides the highest
// confidence in repository health.
func (c *Client) CheckReadData(ctx context.Context, repo string) error {
	return c.runSimple(ctx, "check", "check", "--read-data", "-r", repo)
}

// Snapshots lists all snapshots in the repository.
func (c *Client) Snapshots(ctx context.Context, repo string) ([]Snapshot, error) {
	var buf bytes.Buffer
	var stderr bytes.Buffer
	cmd, cleanup, err := c.command(ctx, "snapshots", "-r", repo, "--json")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd.Stdout = &buf
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w\n%s", err, stderr.String())
	}
	return ParseSnapshots(&buf)
}

// Stats returns statistics for the repository.
func (c *Client) Stats(ctx context.Context, repo string) (*Stats, error) {
	var buf bytes.Buffer
	var stderr bytes.Buffer
	cmd, cleanup, err := c.command(ctx, "stats", "-r", repo, "--json")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd.Stdout = &buf
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic stats: %w\n%s", err, stderr.String())
	}
	return ParseStats(&buf)
}

// Restore restores a snapshot to the given target path.
func (c *Client) Restore(ctx context.Context, repo, snapshotID, targetPath string) error {
	return c.runSimple(ctx, "restore", "restore", snapshotID, "-r", repo, "--target", targetPath)
}

func (c *Client) runSimple(ctx context.Context, op string, args ...string) error {
	var stderr bytes.Buffer
	cmd, cleanup, err := c.command(ctx, args...)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic %s: %w\n%s", op, err, stderr.String())
	}
	return nil
}

func (c *Client) command(ctx context.Context, args ...string) (*exec.Cmd, func(), error) {
	f, err := os.CreateTemp("", "buoy-password-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create password temp file: %w", err)
	}
	if _, err := f.WriteString(c.password); err != nil {
		f.Close()           //nolint:errcheck
		os.Remove(f.Name()) //nolint:errcheck
		return nil, nil, fmt.Errorf("write password temp file: %w", err)
	}
	f.Close() //nolint:errcheck

	args = append([]string{"--password-file", f.Name()}, args...)
	cmd := exec.CommandContext(ctx, c.binPath, args...)
	cmd.Env = append(os.Environ(),
		"RESTIC_COMPRESSION="+c.compression,
		"RESTIC_PROGRESS_FPS=1",
	)

	cleanup := func() {
		os.Remove(f.Name()) //nolint:errcheck
	}
	return cmd, cleanup, nil
}
