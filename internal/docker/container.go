package docker

import (
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
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
	Retention       RetentionConfig
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

// RetentionConfig defines how many snapshots to keep for each time period.
type RetentionConfig struct {
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	KeepWithin  string
}

// ParseBackupConfig extracts backup configuration from Docker labels.
// Unrecognized labels are ignored. Missing optional labels fall back to defaults.
func ParseBackupConfig(labels map[string]string, defaultSchedule, defaultRetention string) BackupConfig {
	cfg := BackupConfig{
		StopTimeout: 30 * time.Second,
		Retention: RetentionConfig{
			KeepDaily: 7,
		},
	}

	if v, ok := labels["buoy.enabled"]; ok {
		cfg.Enabled, _ = strconv.ParseBool(v)
	}
	if v, ok := labels["buoy.schedule"]; ok {
		cfg.Schedule = v
	} else {
		cfg.Schedule = defaultSchedule
	}
	if v, ok := labels["buoy.repos"]; ok {
		cfg.ReposOverride = splitAndTrim(v)
	}
	if v, ok := labels["buoy.stop-before-backup"]; ok {
		cfg.StopBefore, _ = strconv.ParseBool(v)
	}
	if v, ok := labels["buoy.stop-timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.StopTimeout = d
		}
	}
	if v, ok := labels["buoy.include-volumes"]; ok {
		cfg.IncludeVolumes = splitAndTrim(v)
	}
	if v, ok := labels["buoy.include-mounts"]; ok {
		cfg.IncludeMounts = splitAndTrim(v)
	}
	if v, ok := labels["buoy.exclude-volumes"]; ok {
		cfg.ExcludeVolumes = splitAndTrim(v)
	}
	if v, ok := labels["buoy.exclude-mounts"]; ok {
		cfg.ExcludeMounts = splitAndTrim(v)
	}
	if v, ok := labels["buoy.exclude-patterns"]; ok {
		cfg.ExcludePatterns = splitAndTrim(v)
	}
	if v, ok := labels["buoy.files"]; ok {
		cfg.Files = splitAndTrim(v)
	}
	if v, ok := labels["buoy.tags"]; ok {
		cfg.Tags = splitAndTrim(v)
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

func parseRetention(labels map[string]string, defaultRetention string, rc *RetentionConfig) {
	v, ok := labels["buoy.retention"]
	if !ok {
		v = defaultRetention
	}
	if v == "" {
		return
	}
	for _, part := range splitAndTrim(v) {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		val, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil {
			if strings.TrimSpace(kv[0]) == "keep-within" {
				rc.KeepWithin = strings.TrimSpace(kv[1])
			}
			continue
		}
		switch strings.TrimSpace(kv[0]) {
		case "keep-daily":
			rc.KeepDaily = val
		case "keep-weekly":
			rc.KeepWeekly = val
		case "keep-monthly":
			rc.KeepMonthly = val
		case "keep-yearly":
			rc.KeepYearly = val
		}
	}
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// LogAttrs returns slog attributes for structured logging.
func (c *Container) LogAttrs() []any {
	if c.ComposeProject != "" {
		return []any{"project", c.ComposeProject, "service", c.ComposeService}
	}
	return []any{"container", c.Name}
}
