package hook

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/depado/buoy/internal/docker"
)

func ExecOnHost(ctx context.Context, command string, logger *slog.Logger) error {
	logger.Debug("executing host command")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("host command failed: %w\n%s", err, string(out))
	}
	logger.Debug("host command completed")
	return nil
}

func ExecInContainer(ctx context.Context, d *docker.Client, containerID, command string, logger *slog.Logger) error {
	logger.Debug("executing command in container")
	exitCode, err := d.ExecInContainer(ctx, containerID, command)
	if err != nil {
		return fmt.Errorf("exec in container: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("exec exited with code %d", exitCode)
	}
	logger.Debug("command in container completed")
	return nil
}
