package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"github.com/spf13/viper"
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
		slog.Warn("unrecognized log level, fallback to info", "level", c.Log.Level)
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
		handler = tint.NewHandler(os.Stderr, &tint.Options{
			Level:      level,
			AddSource:  c.Log.Source,
			TimeFormat: time.DateTime,
			NoColor:    noColor,
		})
	default:
		handler = slog.NewJSONHandler(os.Stderr, opts)
		slog.Warn("unrecognized log format, fallback to json", "format", c.Log.Format)
	}

	return slog.New(handler)
}

func NewConf() (*Conf, error) {
	viper.AutomaticEnv()
	viper.SetEnvPrefix("buoy")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	if viper.GetString("conf") != "" {
		viper.SetConfigFile(viper.GetString("conf"))
	} else {
		viper.SetConfigName("conf")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/config/")
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("unable to read config file: %w", err)
		}
	}
	conf := &Conf{}
	if err := viper.Unmarshal(conf); err != nil {
		return conf, fmt.Errorf("unable to unmarshal conf: %w", err)
	}

	return conf, nil
}
