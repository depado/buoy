package hook

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/depado/buoy/internal/docker"
)

type Executor struct {
	docker *docker.Client
}

func New(docker *docker.Client) *Executor {
	return &Executor{docker: docker}
}

func (e *Executor) ExecOnHost(ctx context.Context, command string, logger *slog.Logger) error {
	logger.Debug("executing host command")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("host command failed: %w\n%s", err, string(out))
	}
	logger.Debug("host command completed")
	return nil
}

func (e *Executor) ExecInContainer(ctx context.Context, containerID, command string, logger *slog.Logger) error {
	logger.Debug("executing command in container")
	exitCode, err := e.docker.ExecInContainer(ctx, containerID, command)
	if err != nil {
		return fmt.Errorf("exec in container: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("exec exited with code %d", exitCode)
	}
	logger.Debug("command in container completed")
	return nil
}
