package config

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"

	"github.com/depado/buoy/internal/types"
)

type LogConf struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Source bool   `mapstructure:"source"`
	Color  string `mapstructure:"color"`
}

type DaemonConf struct {
	Concurrency       int    `mapstructure:"concurrency"`
	DefaultSchedule   string `mapstructure:"default_schedule"`
	DefaultRetention  string `mapstructure:"default_retention"`
	ResyncInterval    string `mapstructure:"resync_interval"`
	ExecTimeout       string `mapstructure:"exec_timeout"`
	HealthWaitTimeout string `mapstructure:"health_wait_timeout"`
	CheckSchedule     string `mapstructure:"check_schedule"`
	BackupTimeout     string `mapstructure:"backup_timeout"`
	DBPath            string `mapstructure:"db_path"`
}

type DockerConf struct {
	Host string `mapstructure:"host"`
}

type RepoConfig struct {
	URL      string `mapstructure:"url"`
	Password string `mapstructure:"password"`
}

type ResticConf struct {
	BinaryPath  string                `mapstructure:"binary_path"`
	Password    string                `mapstructure:"password"`
	Compression string                `mapstructure:"compression"`
	Repos       map[string]RepoConfig `mapstructure:"repos"`
}

var repoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func ToRepoRefs(repos map[string]RepoConfig) ([]types.RepoRef, []string) {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)

	refs := make([]types.RepoRef, 0, len(names))
	list := make([]string, 0, len(names))
	for _, name := range names {
		refs = append(refs, types.RepoRef{Name: name, URL: repos[name].URL})
		list = append(list, name+":"+repos[name].URL)
	}
	return refs, list
}

type NotifyConf struct {
	Urls  []string `mapstructure:"urls"`
	Level string   `mapstructure:"level"`
}

type APIConf struct {
	Enabled bool   `mapstructure:"enabled"`
	Host    string `mapstructure:"host"`
	Port    int    `mapstructure:"port"`
	Token   string `mapstructure:"token"`
}

type OtelConf struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
	Protocol string `mapstructure:"protocol"`
	Insecure bool   `mapstructure:"insecure"` // skip TLS, default false
}

type Conf struct {
	Log    LogConf    `mapstructure:"log"`
	Daemon DaemonConf `mapstructure:"daemon"`
	Docker DockerConf `mapstructure:"docker"`
	Restic ResticConf `mapstructure:"restic"`
	Notify NotifyConf `mapstructure:"notify"`
	API    APIConf    `mapstructure:"api"`
	Otel   OtelConf   `mapstructure:"otel"`
}

func NewLogger(c *Conf) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(c.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: c.Log.Source,
	}

	var handler slog.Handler
	switch strings.ToLower(c.Log.Format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "text", "console":
		var noColor bool
		switch strings.ToLower(c.Log.Color) {
		case "always":
			noColor = false
		case "never":
			noColor = true
		default:
			noColor = !isatty.IsTerminal(os.Stderr.Fd())
		}
		handler = tint.NewTextHandler(os.Stderr, &tint.Options{Level: level, AddSource: c.Log.Source, TimeFormat: time.DateTime, NoColor: noColor})
	default:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

const envRepoPrefix = "BUOY_RESTIC_REPO_"

func ScanEnvRepos(rc *ResticConf) {
	if rc.Repos == nil {
		rc.Repos = make(map[string]RepoConfig)
	}
	discovered := make(map[string]RepoConfig)
	for _, kv := range os.Environ() {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if !strings.HasPrefix(key, envRepoPrefix) {
			continue
		}
		suffix := key[len(envRepoPrefix):]

		name, field, ok := parseRepoSuffix(suffix)
		if !ok {
			continue
		}

		if _, exists := rc.Repos[name]; exists {
			continue
		}

		c := discovered[name]
		if field == "url" {
			c.URL = val
		} else {
			c.Password = val
		}
		discovered[name] = c
	}

	for name, c := range discovered {
		if _, exists := rc.Repos[name]; exists {
			continue
		}
		rc.Repos[name] = c
	}
}

func parseRepoSuffix(suffix string) (name, field string, ok bool) {
	const urlSuffix = "_URL"
	const passwordSuffix = "_PASSWORD"

	upper := strings.ToUpper(suffix)
	switch {
	case strings.HasSuffix(upper, urlSuffix):
		name = strings.ToLower(suffix[:len(suffix)-len(urlSuffix)])
		field = "url"
	case strings.HasSuffix(upper, passwordSuffix):
		name = strings.ToLower(suffix[:len(suffix)-len(passwordSuffix)])
		field = "password"
	default:
		return "", "", false
	}

	if !repoNamePattern.MatchString(name) {
		return "", "", false
	}
	return name, field, true
}

func (rc *ResticConf) Validate() error {
	if len(rc.Repos) == 0 {
		return fmt.Errorf("restic.repos is required: at least one named repo must be configured")
	}

	seenURLs := map[string]bool{}
	for name, repo := range rc.Repos {
		if !repoNamePattern.MatchString(name) {
			return fmt.Errorf("repo name %q is invalid: must match %s", name, repoNamePattern.String())
		}
		if repo.URL == "" {
			return fmt.Errorf("repo %q has an empty URL", name)
		}
		if seenURLs[repo.URL] {
			return fmt.Errorf("duplicate repo URL %q (repo %q)", repo.URL, name)
		}
		seenURLs[repo.URL] = true

		if repo.Password == "" && rc.Password == "" {
			return fmt.Errorf("repo %q has no password and no global restic.password is set", name)
		}
	}
	return nil
}

func (rc *ResticConf) PasswordFor(name string) string {
	if c, ok := rc.Repos[name]; ok && c.Password != "" {
		return c.Password
	}
	return rc.Password
}

func (rc *ResticConf) PasswordForURL(url string) string {
	for _, c := range rc.Repos {
		if c.Password == "" {
			continue
		}
		if url == c.URL || (strings.HasPrefix(url, c.URL) && url[len(c.URL)] == '/') {
			return c.Password
		}
	}
	return rc.Password
}

func (rc *ResticConf) PasswordForEntry(repoName, url string) string {
	if repoName != "" {
		return rc.PasswordFor(repoName)
	}
	return rc.PasswordForURL(url)
}
