package docker

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/types"
)

// Container represents a Docker container with its backup-relevant metadata.
type Container struct {
	ID             string
	Name           string
	Image          string
	State          string
	ExitCode       int
	Health         *container.Health
	Labels         map[string]string
	Mounts         []Mount
	ComposeProject string
	ComposeService string
}

// Mount represents a volume or bind mount attached to a container.
type Mount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	Mode        string
	RW          bool
}

// IsComposeStack reports whether this container is part of a Docker Compose stack.
func (c *Container) IsComposeStack() bool {
	return c.ComposeProject != ""
}

// RepoPath returns the restic repository path for this container.
// For compose stacks, it uses <base>/<project>/<service>.
// For standalone containers, it uses <base>/<name>.
func (c *Container) RepoPath(base string) string {
	if c.IsComposeStack() {
		return base + "/" + c.ComposeProject + "/" + c.ComposeService
	}
	return base + "/" + strings.TrimPrefix(c.Name, "/")
}

// BackupConfig holds the parsed backup configuration from container labels.
type BackupConfig struct {
	Enabled         bool
	Schedule        string
	ReposOverride   []string
	Retention       types.RetentionPolicy
	StopBefore      bool
	StopTimeout     time.Duration
	IncludeVolumes  []string
	ExcludeVolumes  []string
	IncludeMounts   []string
	ExcludeMounts   []string
	ExcludePatterns []string
	Files           []string
	Tags            []string
	PreBackupCmd    string
	PostBackupCmd   string
	PreBackupExec   string
	PostBackupExec  string
}

// ParseBackupConfig extracts backup configuration from Docker labels.
// Unrecognized labels are ignored. Missing optional labels fall back to defaults.
func ParseBackupConfig(labels map[string]string, defaultSchedule, defaultRetention string) BackupConfig {
	cfg := BackupConfig{
		StopTimeout: 30 * time.Second,
		Retention: types.RetentionPolicy{
			KeepDaily: 7,
		},
	}

	if v, ok := labels["buoy.enabled"]; ok {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			slog.Warn("invalid buoy.enabled, defaulting to false", "value", v)
		}
		cfg.Enabled = enabled
	}
	if v, ok := labels["buoy.schedule"]; ok {
		cfg.Schedule = v
	} else {
		cfg.Schedule = defaultSchedule
	}
	if v, ok := labels["buoy.repos"]; ok {
		cfg.ReposOverride = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.stop-before-backup"]; ok {
		stop, err := strconv.ParseBool(v)
		if err != nil {
			slog.Warn("invalid buoy.stop-before-backup, defaulting to false", "value", v)
		}
		cfg.StopBefore = stop
	}
	if v, ok := labels["buoy.stop-timeout"]; ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			slog.Warn("invalid buoy.stop-timeout, using default 30s", "value", v)
		} else {
			cfg.StopTimeout = d
		}
	}
	if v, ok := labels["buoy.include-volumes"]; ok {
		cfg.IncludeVolumes = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.include-mounts"]; ok {
		cfg.IncludeMounts = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.exclude-volumes"]; ok {
		cfg.ExcludeVolumes = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.exclude-mounts"]; ok {
		cfg.ExcludeMounts = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.exclude-patterns"]; ok {
		cfg.ExcludePatterns = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.files"]; ok {
		cfg.Files = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.tags"]; ok {
		cfg.Tags = types.SplitTrim(v)
	}
	if v, ok := labels["buoy.pre-backup-cmd"]; ok {
		cfg.PreBackupCmd = v
	}
	if v, ok := labels["buoy.post-backup-cmd"]; ok {
		cfg.PostBackupCmd = v
	}
	if v, ok := labels["buoy.pre-backup-exec"]; ok {
		cfg.PreBackupExec = v
	}
	if v, ok := labels["buoy.post-backup-exec"]; ok {
		cfg.PostBackupExec = v
	}

	parseRetention(labels, defaultRetention, &cfg.Retention)

	return cfg
}

func parseRetention(labels map[string]string, defaultRetention string, rc *types.RetentionPolicy) {
	v, ok := labels["buoy.retention"]
	if !ok {
		v = defaultRetention
	}
	if v == "" {
		return
	}
	parsed := types.ParseRetentionPolicy(v)
	if parsed.KeepLast > 0 {
		rc.KeepLast = parsed.KeepLast
	}
	if parsed.KeepHourly > 0 {
		rc.KeepHourly = parsed.KeepHourly
	}
	if parsed.KeepDaily > 0 {
		rc.KeepDaily = parsed.KeepDaily
	}
	if parsed.KeepWeekly > 0 {
		rc.KeepWeekly = parsed.KeepWeekly
	}
	if parsed.KeepMonthly > 0 {
		rc.KeepMonthly = parsed.KeepMonthly
	}
	if parsed.KeepYearly > 0 {
		rc.KeepYearly = parsed.KeepYearly
	}
	if parsed.KeepWithin != "" {
		rc.KeepWithin = parsed.KeepWithin
	}
}

// LogAttrs returns slog attributes for structured logging.
func (c *Container) LogAttrs() []any {
	if c.ComposeProject != "" {
		return []any{"project", c.ComposeProject, "service", c.ComposeService}
	}
	return []any{"container", c.Name}
}
