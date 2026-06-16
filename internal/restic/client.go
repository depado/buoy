package restic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Client wraps the restic binary and provides methods for all backup operations.
type Client struct {
	binPath  string
	password string
}

// New creates a new restic Client using the given binary path and repository password.
func New(binPath, password string) *Client {
	return &Client{
		binPath:  binPath,
		password: password,
	}
}

// Init initializes a new restic repository at the given location.
func (c *Client) Init(ctx context.Context, repo string) error {
	cmd := c.command(ctx, "init", "-r", repo)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic init: %w", err)
	}
	return nil
}

// RepoExists checks whether a restic repository exists at the given location.
// Returns false with no error if the repository does not exist (exit code 10).
func (c *Client) RepoExists(ctx context.Context, repo string) (bool, error) {
	cmd := c.command(ctx, "cat", "config", "-r", repo)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 10 {
			return false, nil
		}
		return false, fmt.Errorf("restic cat config: %w", err)
	}
	return true, nil
}

// Unlock removes stale locks from a repository. Safe to call before every backup.
func (c *Client) Unlock(ctx context.Context, repo string) error {
	cmd := c.command(ctx, "unlock", "-r", repo)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic unlock: %w", err)
	}
	return nil
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
		defer os.Remove(f.Name())
		if _, err := f.WriteString(strings.Join(opts.Files, "\n")); err != nil {
			f.Close()
			return nil, fmt.Errorf("write files temp file: %w", err)
		}
		f.Close()
		tmpFile = f.Name()
		args = append(args, "--files-from", tmpFile)
	} else {
		args = append(args, paths...)
	}

	cmd := c.command(ctx, args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start restic backup: %w", err)
	}

	var errors []BackupError
	summary, parseErr := ParseBackupStream(stdout, stderrPipe, nil, func(e BackupError) {
		errors = append(errors, e)
	})

	waitErr := cmd.Wait()

	if parseErr != nil {
		return summary, parseErr
	}
	if waitErr != nil {
		return summary, fmt.Errorf("restic backup: %w", waitErr)
	}

	return summary, nil
}

// Forget applies a retention policy to remove old snapshots.
// Uses --group-by host,tags to match the backup command's grouping.
func (c *Client) Forget(ctx context.Context, repo string, policy RetentionPolicy) error {
	args := []string{"forget", "-r", repo, "--json", "--group-by", "host,tags"}
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

	cmd := c.command(ctx, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic forget: %w", err)
	}
	return nil
}

// Prune removes unreferenced data from the repository after forget.
func (c *Client) Prune(ctx context.Context, repo string) error {
	cmd := c.command(ctx, "prune", "-r", repo)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic prune: %w", err)
	}
	return nil
}

// Snapshots lists all snapshots in the repository.
func (c *Client) Snapshots(ctx context.Context, repo string) ([]Snapshot, error) {
	var buf bytes.Buffer
	cmd := c.command(ctx, "snapshots", "-r", repo, "--json")
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w", err)
	}
	return ParseSnapshots(&buf)
}

// Stats returns statistics for the repository.
func (c *Client) Stats(ctx context.Context, repo string) (*Stats, error) {
	var buf bytes.Buffer
	cmd := c.command(ctx, "stats", "-r", repo, "--json")
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic stats: %w", err)
	}
	return ParseStats(&buf)
}

// Restore restores a snapshot to the given target path.
func (c *Client) Restore(ctx context.Context, repo, snapshotID, targetPath string) error {
	cmd := c.command(ctx, "restore", snapshotID, "-r", repo, "--target", targetPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic restore: %w", err)
	}
	return nil
}

func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.binPath, args...)
	cmd.Env = append(os.Environ(),
		"RESTIC_PASSWORD="+c.password,
		"RESTIC_COMPRESSION=auto",
		"RESTIC_PROGRESS_FPS=1",
	)
	return cmd
}
