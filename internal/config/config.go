package config

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
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

type ResticConf struct {
	BinaryPath  string   `mapstructure:"binary_path"`
	Password    string   `mapstructure:"password"`
	Compression string   `mapstructure:"compression"`
	Repos       []string `mapstructure:"repos"`
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

type Conf struct {
	Log    LogConf    `mapstructure:"log"`
	Daemon DaemonConf `mapstructure:"daemon"`
	Docker DockerConf `mapstructure:"docker"`
	Restic ResticConf `mapstructure:"restic"`
	Notify NotifyConf `mapstructure:"notify"`
	API    APIConf    `mapstructure:"api"`
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
