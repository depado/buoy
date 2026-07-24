package docker

import (
	"log/slog"
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

// MountEntry is a single entry in buoy.include, optionally named for
// per-mount backup option overrides.
type MountEntry struct {
	Name string
	Key  string
}

// MountBackupOpts holds per-mount backup configuration for a named include entry.
type MountBackupOpts struct {
	Files   []string
	Exclude []string
	Tags    []string
}

// BackupConfig holds the parsed backup configuration from container labels.
type BackupConfig struct {
	Enabled       bool
	Schedule      string
	ReposOverride []string
	Retention     types.RetentionPolicy
	Password      string

	StopBefore  bool
	StopTimeout time.Duration

	Include []MountEntry
	Exclude []string

	BackupFiles   []string
	BackupExclude []string
	BackupTags    []string

	MountOpts map[string]MountBackupOpts

	HookPreCmd   string
	HookPostCmd  string
	HookPreExec  string
	HookPostExec string
}

// ResolveMountBackup returns the effective files, exclude patterns, and tags
// for a named mount entry. Per-mount values replace globals for files/exclude;
// tags are appended.
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

// ParseBackupConfig extracts backup configuration from Docker labels.
// Unrecognized labels are ignored. Missing optional labels fall back to defaults.
func ParseBackupConfig(labels map[string]string, defaultSchedule, defaultRetention string) BackupConfig {
	cfg := BackupConfig{
		StopTimeout: 30 * time.Second,
		MountOpts:   make(map[string]MountBackupOpts),
	}

	cfg.Enabled = getBool(labels, "buoy.enabled", false)
	cfg.Schedule = getString(labels, "buoy.schedule", defaultSchedule)
	cfg.ReposOverride = getSlice(labels, "buoy.repos")
	cfg.Password = getString(labels, "buoy.password", "")
	cfg.StopBefore = getBool(labels, "buoy.stop-before", false)
	cfg.StopTimeout = getDuration(labels, "buoy.stop-timeout", 30*time.Second)

	if v, ok := labels["buoy.include"]; ok {
		cfg.Include = parseIncludeEntries(v)
	}
	cfg.Exclude = getSlice(labels, "buoy.exclude")
	cfg.BackupFiles = getSlice(labels, "buoy.backup.files")
	cfg.BackupExclude = getSlice(labels, "buoy.backup.exclude")
	cfg.BackupTags = getSlice(labels, "buoy.backup.tags")

	cfg.HookPreCmd = getString(labels, "buoy.hook.pre.cmd", "")
	cfg.HookPostCmd = getString(labels, "buoy.hook.post.cmd", "")
	cfg.HookPreExec = getString(labels, "buoy.hook.pre.exec", "")
	cfg.HookPostExec = getString(labels, "buoy.hook.post.exec", "")

	parseBackupMountOpts(labels, cfg.MountOpts)
	parseRetention(labels, defaultRetention, &cfg.Retention)

	return cfg
}

func parseIncludeEntries(raw string) []MountEntry {
	parts := types.SplitTrim(raw)
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

func parseBackupMountOpts(labels map[string]string, opts map[string]MountBackupOpts) {
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
			entry.Files = types.SplitTrim(v)
		case "exclude":
			entry.Exclude = types.SplitTrim(v)
		case "tags":
			entry.Tags = types.SplitTrim(v)
		default:
			continue
		}
		opts[name] = entry
	}
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
	setNonZero(&rc.KeepLast, parsed.KeepLast)
	setNonZero(&rc.KeepHourly, parsed.KeepHourly)
	setNonZero(&rc.KeepDaily, parsed.KeepDaily)
	setNonZero(&rc.KeepWeekly, parsed.KeepWeekly)
	setNonZero(&rc.KeepMonthly, parsed.KeepMonthly)
	setNonZero(&rc.KeepYearly, parsed.KeepYearly)
	if parsed.KeepWithin != "" {
		rc.KeepWithin = parsed.KeepWithin
	}
}

// MountMatches checks whether a mount passes the include/exclude filter.
// It returns the matched entry name (for per-mount backup overrides) and whether the mount is included.
// When an unnamed include entry matches a volume by its Docker name, the volume name is
// automatically used as the matched entry name.
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

// LogAttrs returns slog attributes for structured logging.
func (c *Container) LogAttrs() []any {
	if c.ComposeProject != "" {
		return []any{"project", c.ComposeProject, "service", c.ComposeService}
	}
	return []any{"container", c.Name}
}
