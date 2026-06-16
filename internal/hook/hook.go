package hook

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/depado/buoy/internal/docker"
)

// Executor runs pre- and post-backup hooks, either on the host or inside containers.
type Executor struct {
	docker *docker.Client
}

// New creates a new Executor.
func New(docker *docker.Client) *Executor {
	return &Executor{docker: docker}
}

// ExecOnHost runs a shell command on the host using /bin/sh -c.
func (e *Executor) ExecOnHost(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("host command failed: %w\n%s", err, string(out))
	}
	return nil
}

// ExecInContainer runs a command inside a container via docker exec.
// Returns an error if the command exits with a non-zero code.
func (e *Executor) ExecInContainer(ctx context.Context, containerID, command string) error {
	exitCode, err := e.docker.ExecInContainer(ctx, containerID, command)
	if err != nil {
		return fmt.Errorf("exec in container: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("exec exited with code %d", exitCode)
	}
	return nil
}
