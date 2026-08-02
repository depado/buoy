package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/depado/buoy/internal/types"
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

type passwordCtxKey struct{}

// WithPassword returns a context that carries a password override for a single
// restic invocation. When set, it replaces the client's default password.
func WithPassword(ctx context.Context, password string) context.Context {
	return context.WithValue(ctx, passwordCtxKey{}, password)
}

func passwordFromCtx(ctx context.Context, fallback string) string {
	if pw, ok := ctx.Value(passwordCtxKey{}).(string); ok && pw != "" {
		return pw
	}
	return fallback
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

	if len(opts.Files) > 0 {
		f, err := writeTempFile("buoy-files-*.txt", strings.Join(opts.Files, "\n"))
		if err != nil {
			return nil, err
		}
		defer os.Remove(f) //nolint:errcheck
		args = append(args, "--files-from", f)
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
	summary, exitErr, parseErr := parseBackupStream(stdout, func(e BackupError) {
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
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		var e ExitError
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.MessageType == "exit_error" && e.Code > 0 {
			return &e
		}
	}
	return nil
}

// Forget applies a retention policy to remove old snapshots.
// Uses --group-by host,tags to match the backup command's grouping.
func (c *Client) Forget(ctx context.Context, repo string, policy types.RetentionPolicy, hostname string) error {
	return c.runSimple(ctx, "forget", forgetArgs(repo, policy, hostname)...)
}

func forgetArgs(repo string, policy types.RetentionPolicy, hostname string) []string {
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

// Stats returns statistics for the repository.
// Runs both restore-size and raw-data modes to collect all available fields.
// TotalSize and compression fields come from raw-data mode (actual repo data).
// TotalFileCount and SnapshotsCount come from restore-size mode.
func (c *Client) Stats(ctx context.Context, repo string) (*types.Stats, error) {
	restoreStats, err := c.statsMode(ctx, repo)
	if err != nil {
		return nil, err
	}
	rawStats, rawErr := c.statsMode(ctx, repo, "--mode", "raw-data")
	if rawErr != nil {
		return restoreStats, fmt.Errorf("raw-data stats: %w", rawErr)
	}
	restoreStats.TotalSize = rawStats.TotalSize
	restoreStats.TotalBlobCount = rawStats.TotalBlobCount
	restoreStats.TotalUncompressedSize = rawStats.TotalUncompressedSize
	restoreStats.CompressionRatio = rawStats.CompressionRatio
	restoreStats.CompressionProgress = rawStats.CompressionProgress
	restoreStats.CompressionSpaceSaving = rawStats.CompressionSpaceSaving
	return restoreStats, nil
}

func (c *Client) statsMode(ctx context.Context, repo string, extraArgs ...string) (*types.Stats, error) {
	args := make([]string, 0, 4+len(extraArgs))
	args = append(args, "stats", "-r", repo, "--json")
	args = append(args, extraArgs...)
	var buf bytes.Buffer
	var stderr bytes.Buffer
	cmd, cleanup, err := c.command(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd.Stdout = &buf
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("restic stats: %w\n%s", err, stderr.String())
	}
	return parseStats(&buf)
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
	f, err := writeTempFile("buoy-password-*", passwordFromCtx(ctx, c.password))
	if err != nil {
		return nil, nil, err
	}

	args = append([]string{"--password-file", f}, args...)
	cmd := exec.CommandContext(ctx, c.binPath, args...)
	setSysProcAttr(cmd)
	cmd.Env = append(os.Environ(),
		"RESTIC_COMPRESSION="+c.compression,
		"RESTIC_PROGRESS_FPS=1",
	)

	cleanup := func() {
		if err := os.Remove(f); err != nil {
			slog.Warn("failed to remove password temp file", "error", err)
		}
	}
	return cmd, cleanup, nil
}

// writeTempFile creates a temporary file with the given pattern and content,
// returning its path. The caller is responsible for removing the file.
func writeTempFile(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file %q: %w", pattern, err)
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(f.Name()) //nolint:errcheck
		return "", fmt.Errorf("chmod temp file %q: %w", pattern, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name()) //nolint:errcheck
		return "", fmt.Errorf("write temp file %q: %w", pattern, err)
	}
	return f.Name(), nil
}
