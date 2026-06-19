package notify

import (
	"fmt"
	"log/slog"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// Level controls which events trigger notifications.
type Level int

const (
	LevelNone  Level = iota
	LevelError
	LevelAll
)

// ParseLevel converts a string to a notification Level.
// Unknown values default to LevelNone.
func ParseLevel(s string) Level {
	switch s {
	case "error":
		return LevelError
	case "all":
		return LevelAll
	default:
		return LevelNone
	}
}

// Notifier sends backup failure notifications via shoutrrr.
type Notifier struct {
	sender *router.ServiceRouter
	level  Level
	logger *slog.Logger
}

// New creates a Notifier from shoutrrr service URLs. If urls is empty or
// level is LevelNone, returns a nil Notifier (notifications disabled).
// Unknown services in the URLs cause a creation error.
func New(urls []string, level Level, logger *slog.Logger) (*Notifier, error) {
	if level == LevelNone || len(urls) == 0 {
		return nil, nil
	}

	sender, err := shoutrrr.CreateSender(urls...)
	if err != nil {
		return nil, fmt.Errorf("create shoutrrr sender: %w", err)
	}

	return &Notifier{
		sender: sender,
		level:  level,
		logger: logger,
	}, nil
}

// SendBackupError sends a notification about a backup failure.
// The notifier handles shoutrrr errors internally — they are logged as
// warnings but never propagated to the caller.
func (n *Notifier) SendBackupError(ctrName, msg string) {
	if n == nil {
		return
	}
	title := fmt.Sprintf("buoy backup failed: %s", ctrName)
	params := &types.Params{"title": title}
	errs := n.sender.Send(msg, params)
	for i, e := range errs {
		if e != nil {
			n.logger.Warn("notification failed", "service_index", i, "error", e)
		}
	}
}

// SendInfo sends an informational notification. Only delivered when Level is
// LevelAll.
func (n *Notifier) SendInfo(title, msg string) {
	if n == nil || n.level < LevelAll {
		return
	}
	params := &types.Params{"title": title}
	errs := n.sender.Send(msg, params)
	for i, e := range errs {
		if e != nil {
			n.logger.Warn("notification failed", "service_index", i, "error", e)
		}
	}
}
