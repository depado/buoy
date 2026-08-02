package types

import (
	"log/slog"
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

// MountEntry is a single entry in buoy.include, optionally named for
// per-mount backup option overrides.
type MountEntry struct {
	Name string
	Key  string
}

// BackupConfig holds the parsed backup configuration from container labels.
type BackupConfig struct {
	Enabled       bool
	Schedule      string
	ReposOverride []string
	Retention     RetentionPolicy
	Password      string

	StopBefore  bool
	StopTimeout time.Duration

	Include []MountEntry
	Exclude []string

	BackupFiles   []string
	BackupExclude []string
	BackupTags    []string

	MountOpts map[string]mountBackupOpts

	HookPreCmd   string
	HookPostCmd  string
	HookPreExec  string
	HookPostExec string
}

// mountBackupOpts holds per-mount backup configuration for a named include entry.
type mountBackupOpts struct {
	Files   []string
	Exclude []string
	Tags    []string
}

// IsComposeStack reports whether this container is part of a Docker Compose stack.
func (c *Container) IsComposeStack() bool {
	return c.ComposeProject != ""
}

// RepoPath returns the restic repository path for this container.
func (c *Container) RepoPath(base string) string {
	if c.IsComposeStack() {
		return base + "/" + c.ComposeProject + "/" + c.ComposeService
	}
	return base + "/" + strings.TrimPrefix(c.Name, "/")
}

// LogAttrs returns slog attributes for structured logging.
func (c *Container) LogAttrs() []any {
	if c.ComposeProject != "" {
		return []any{"project", c.ComposeProject, "service", c.ComposeService}
	}
	return []any{"container", c.Name}
}

// ResolveMountBackup returns the effective files, exclude patterns, and tags
// for a named mount entry.
func (c BackupConfig) ResolveMountBackup(name string) (files, excludes, tags []string) {
	files, excludes = c.BackupFiles, c.BackupExclude
	tags = make([]string, 0, len(c.BackupTags)+len(c.MountOpts[name].Tags))
	tags = append(tags, c.BackupTags...)

	if name == "" {
		return
	}
	opts, ok := c.MountOpts[name]
	if !ok {
		return
	}
	if len(opts.Files) > 0 {
		files = opts.Files
	}
	if len(opts.Exclude) > 0 {
		excludes = opts.Exclude
	}
	tags = append(tags, opts.Tags...)
	return
}

// MountMatches checks whether a mount passes the include/exclude filter.
func MountMatches(m Mount, include []MountEntry, exclude []string) (matchedName string, ok bool) {
	if len(include) > 0 {
		for _, entry := range include {
			if entry.Key == m.Name {
				if entry.Name != "" {
					return entry.Name, true
				}
				return m.Name, true
			}
			if entry.Key == m.Source || entry.Key == m.Destination {
				return entry.Name, true
			}
		}
		return "", false
	}
	for _, ex := range exclude {
		if ex == m.Name || ex == m.Source || ex == m.Destination {
			return "", false
		}
	}
	return "", true
}

// ParseBackupConfig extracts backup configuration from Docker labels.
func ParseBackupConfig(labels map[string]string, defaultSchedule, defaultRetention string) BackupConfig {
	cfg := BackupConfig{
		StopTimeout: 30 * time.Second,
		MountOpts:   make(map[string]mountBackupOpts),
	}

	cfg.Enabled, _ = strconv.ParseBool(labels["buoy.enabled"])
	cfg.Schedule = defaultSchedule
	if v, ok := labels["buoy.schedule"]; ok {
		cfg.Schedule = v
	}
	if v, ok := labels["buoy.repos"]; ok {
		cfg.ReposOverride = SplitTrim(v)
	}
	if v, ok := labels["buoy.password"]; ok {
		cfg.Password = v
	}
	cfg.StopBefore, _ = strconv.ParseBool(labels["buoy.stop-before"])
	cfg.StopTimeout = 30 * time.Second
	if v, ok := labels["buoy.stop-timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.StopTimeout = d
		}
	}

	if v, ok := labels["buoy.include"]; ok {
		cfg.Include = parseIncludeEntries(v)
	}
	if v, ok := labels["buoy.exclude"]; ok {
		cfg.Exclude = SplitTrim(v)
	}
	if v, ok := labels["buoy.backup.files"]; ok {
		cfg.BackupFiles = SplitTrim(v)
	}
	if v, ok := labels["buoy.backup.exclude"]; ok {
		cfg.BackupExclude = SplitTrim(v)
	}
	if v, ok := labels["buoy.backup.tags"]; ok {
		cfg.BackupTags = SplitTrim(v)
	}

	if v, ok := labels["buoy.hook.pre.cmd"]; ok {
		cfg.HookPreCmd = v
	}
	if v, ok := labels["buoy.hook.post.cmd"]; ok {
		cfg.HookPostCmd = v
	}
	if v, ok := labels["buoy.hook.pre.exec"]; ok {
		cfg.HookPreExec = v
	}
	if v, ok := labels["buoy.hook.post.exec"]; ok {
		cfg.HookPostExec = v
	}

	parseBackupMountOpts(labels, cfg.MountOpts)
	parseRetention(labels, defaultRetention, &cfg.Retention)

	return cfg
}

func parseIncludeEntries(raw string) []MountEntry {
	parts := SplitTrim(raw)
	entries := make([]MountEntry, 0, len(parts))
	seen := make(map[string]bool)

	for _, p := range parts {
		name, key := "", p
		if before, after, ok := strings.Cut(p, "="); ok {
			name = strings.TrimSpace(before)
			key = strings.TrimSpace(after)
		}
		if key == "" {
			continue
		}
		if name != "" {
			if seen[name] {
				slog.Warn("duplicate include name, ignoring", "name", name)
				continue
			}
			seen[name] = true
		}
		entries = append(entries, MountEntry{Name: name, Key: key})
	}
	return entries
}

func parseBackupMountOpts(labels map[string]string, opts map[string]mountBackupOpts) {
	const prefix = "buoy.backup."
	for k, v := range labels {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		name, option, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		if name == "" || option == "" {
			continue
		}
		entry := opts[name]
		switch option {
		case "files":
			entry.Files = SplitTrim(v)
		case "exclude":
			entry.Exclude = SplitTrim(v)
		case "tags":
			entry.Tags = SplitTrim(v)
		default:
			continue
		}
		opts[name] = entry
	}
}

func parseRetention(labels map[string]string, defaultRetention string, rc *RetentionPolicy) {
	v, ok := labels["buoy.retention"]
	if !ok {
		v = defaultRetention
	}
	if v == "" {
		return
	}
	parsed := ParseRetentionPolicy(v)
	if parsed.KeepLast != 0 {
		rc.KeepLast = parsed.KeepLast
	}
	if parsed.KeepHourly != 0 {
		rc.KeepHourly = parsed.KeepHourly
	}
	if parsed.KeepDaily != 0 {
		rc.KeepDaily = parsed.KeepDaily
	}
	if parsed.KeepWeekly != 0 {
		rc.KeepWeekly = parsed.KeepWeekly
	}
	if parsed.KeepMonthly != 0 {
		rc.KeepMonthly = parsed.KeepMonthly
	}
	if parsed.KeepYearly != 0 {
		rc.KeepYearly = parsed.KeepYearly
	}
	if parsed.KeepWithin != "" {
		rc.KeepWithin = parsed.KeepWithin
	}
}
