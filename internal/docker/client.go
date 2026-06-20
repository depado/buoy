package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// Client wraps the Docker Engine API client.
type Client struct {
	api *client.Client
}

// New creates a new Docker client connecting to the given host.
// If host is empty, the DOCKER_HOST environment variable is used.
func New(host string) (*Client, error) {
	opts := []client.Opt{}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	} else {
		opts = append(opts, client.FromEnv)
	}

	api, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{api: api}, nil
}

// Close closes the underlying Docker client connection.
func (c *Client) Close() error {
	return c.api.Close()
}

// ListBackupContainers returns all running containers that have buoy.enabled=true.
func (c *Client) ListBackupContainers(ctx context.Context) ([]Container, error) {
	f := client.Filters{}
	f.Add("label", "buoy.enabled=true")
	f.Add("status", "running")

	result, err := c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	containers := make([]Container, 0, len(result.Items))
	for _, s := range result.Items {
		containers = append(containers, containerFromSummary(s))
	}
	return containers, nil
}

// ListContainersByProject returns all containers belonging to the given compose project.
func (c *Client) ListContainersByProject(ctx context.Context, project string) ([]Container, error) {
	f := client.Filters{}
	f.Add("label", "com.docker.compose.project="+project)

	result, err := c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("list containers by project: %w", err)
	}

	containers := make([]Container, 0, len(result.Items))
	for _, s := range result.Items {
		containers = append(containers, containerFromSummary(s))
	}
	return containers, nil
}

// InspectContainer returns detailed information about a container, including mounts.
func (c *Client) InspectContainer(ctx context.Context, id string) (*Container, error) {
	result, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", id, err)
	}
	return containerFromInspect(result.Container), nil
}

// StopContainer stops a container with the given timeout.
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	_, err := c.api.ContainerStop(ctx, id, client.ContainerStopOptions{
		Timeout: &secs,
	})
	if err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

// StartContainer starts a container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	_, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	return nil
}

// ContainerWait blocks until the container reaches the given condition.
func (c *Client) ContainerWait(ctx context.Context, id string, condition container.WaitCondition) error {
	result := c.api.ContainerWait(ctx, id, client.ContainerWaitOptions{
		Condition: condition,
	})

	select {
	case resp := <-result.Result:
		if resp.Error != nil {
			return fmt.Errorf("wait container %s: %s", id, resp.Error.Message)
		}
		return nil
	case err := <-result.Error:
		return fmt.Errorf("wait container %s: %w", id, err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExecInContainer runs a command inside a container and returns its exit code.
func (c *Client) ExecInContainer(ctx context.Context, containerID, command string) (int, error) {
	cmd := []string{"/bin/sh", "-c", command}
	execResp, err := c.api.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		return -1, fmt.Errorf("exec create: %w", err)
	}

	attachResp, err := c.api.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()

	go io.Copy(io.Discard, attachResp.Reader) //nolint:errcheck

	for {
		inspect, err := c.api.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
		if err != nil {
			return -1, fmt.Errorf("exec inspect: %w", err)
		}
		if !inspect.Running {
			return inspect.ExitCode, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Events subscribes to Docker events matching the given filters.
// Returns separate channels for messages and errors.
func (c *Client) Events(ctx context.Context, filters client.Filters) (<-chan events.Message, <-chan error) {
	result := c.api.Events(ctx, client.EventsListOptions{Filters: filters})
	return result.Messages, result.Err
}

// WatchContainer subscribes to Docker events for a specific container.
func (c *Client) WatchContainer(ctx context.Context, containerID string, eventTypes ...string) (<-chan events.Message, <-chan error) {
	f := client.Filters{}
	f.Add("type", "container")
	f.Add("container", containerID)
	for _, et := range eventTypes {
		f.Add("event", et)
	}
	return c.Events(ctx, f)
}

func containerFromSummary(s container.Summary) Container {
	ctr := Container{
		ID:             s.ID,
		Image:          s.Image,
		Labels:         s.Labels,
		State:          string(s.State),
		ComposeProject: s.Labels["com.docker.compose.project"],
		ComposeService: s.Labels["com.docker.compose.service"],
	}
	if len(s.Names) > 0 {
		ctr.Name = strings.TrimPrefix(s.Names[0], "/")
	}
	return ctr
}

func containerFromInspect(c container.InspectResponse) *Container {
	ctr := &Container{
		ID:             c.ID,
		Name:           strings.TrimPrefix(c.Name, "/"),
		Image:          c.Image,
		State:          string(c.State.Status),
		ExitCode:       c.State.ExitCode,
		Health:         c.State.Health,
		Labels:         c.Config.Labels,
		ComposeProject: c.Config.Labels["com.docker.compose.project"],
		ComposeService: c.Config.Labels["com.docker.compose.service"],
	}
	for _, m := range c.Mounts {
		ctr.Mounts = append(ctr.Mounts, Mount{
			Type:        string(m.Type),
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
			Mode:        m.Mode,
			RW:          m.RW,
		})
	}
	return ctr
}
