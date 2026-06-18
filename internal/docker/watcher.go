package docker

import (
	"context"
	"log/slog"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// EventType represents the type of a Docker event.
type EventType string

const (
	EventStart   EventType = "start"
	EventDie     EventType = "die"
	EventDestroy EventType = "destroy"
)

// Event represents a Docker container lifecycle event.
type Event struct {
	Type      EventType
	ID        string
	ActorName string
}

// Watcher watches for Docker container events and emits them on channels.
// It automatically reconnects on stream errors with exponential backoff.
type Watcher struct {
	docker *Client
	logger *slog.Logger
}

// NewWatcher creates a new Watcher that uses the given Docker client.
func NewWatcher(docker *Client, logger *slog.Logger) *Watcher {
	return &Watcher{docker: docker, logger: logger}
}

// Watch starts watching for container start and die events.
// Returns two channels: events and errors. Both are closed when ctx is cancelled.
func (w *Watcher) Watch(ctx context.Context) (<-chan Event, <-chan error) {
	eventsCh := make(chan Event)
	errCh := make(chan error, 1)

	go w.loop(ctx, eventsCh, errCh)
	return eventsCh, errCh
}

func (w *Watcher) loop(ctx context.Context, eventsCh chan<- Event, errCh chan<- error) {
	defer close(eventsCh)
	defer close(errCh)

	backoff := 1 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		f := client.Filters{}
		f.Add("type", "container")
		f.Add("event", "start")
		f.Add("event", "die")
		f.Add("event", "destroy")

		connCtx, connCancel := context.WithCancel(ctx)
		msgs, errs := w.docker.Events(connCtx, f)

		reconnect := w.streamEvents(connCtx, msgs, errs, eventsCh, errCh)
		connCancel()

		if !reconnect {
			return
		}

		select {
		case <-time.After(backoff):
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		case <-ctx.Done():
			return
		}
	}
}

// streamEvents reads from the Docker event stream channels and forwards
// events and errors. Returns true if the stream ended and the caller should
// reconnect; returns false if ctx is cancelled and the caller should shut down.
func (w *Watcher) streamEvents(
	ctx context.Context,
	msgs <-chan events.Message,
	errs <-chan error,
	eventsCh chan<- Event,
	errCh chan<- error,
) bool {
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return true
			}
			eventsCh <- Event{
				Type:      EventType(msg.Action),
				ID:        msg.Actor.ID,
				ActorName: msg.Actor.Attributes["name"],
			}
		case err, ok := <-errs:
			if !ok {
				return true
			}
			if err != nil {
				w.logger.Warn("docker event stream error", "error", err)
				errCh <- err
				return true
			}
		case <-ctx.Done():
			return false
		}
	}
}
