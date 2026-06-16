package cmd

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

// LogConf configures structured logging.
type LogConf struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Source bool   `mapstructure:"source"`
	Color  string `mapstructure:"color"`
}

// DaemonConf configures the buoy daemon.
type DaemonConf struct {
	Concurrency      int    `mapstructure:"concurrency"`
	DefaultSchedule  string `mapstructure:"default_schedule"`
	DefaultRetention string `mapstructure:"default_retention"`
}

// DockerConf configures the Docker Engine connection.
type DockerConf struct {
	Host string `mapstructure:"host"`
}

// ResticConf configures the restic backup engine.
type ResticConf struct {
	BinaryPath  string `mapstructure:"binary_path"`
	Password    string `mapstructure:"password"`
	CacheDir    string `mapstructure:"cache_dir"`
	Compression string `mapstructure:"compression"`
	BaseRepo    string `mapstructure:"base_repo"`
}

// Conf is the top-level configuration for buoy.
type Conf struct {
	Log    LogConf    `mapstructure:"log"`
	Daemon DaemonConf `mapstructure:"daemon"`
	Docker DockerConf `mapstructure:"docker"`
	Restic ResticConf `mapstructure:"restic"`
}

// NewLogger creates a structured logger from the given configuration.
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
		default: // "auto" or empty
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

// NewConf loads configuration from environment variables (BUOY_ prefix),
// config file (conf.yaml), and CLI flags.
func NewConf() (*Conf, error) {
	// Environment variables
	viper.AutomaticEnv()
	viper.SetEnvPrefix("buoy")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	// Configuration file
	if viper.GetString("conf") != "" {
		viper.SetConfigFile(viper.GetString("conf"))
	} else {
		viper.SetConfigName("conf")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/config/")
	}

	viper.ReadInConfig() //nolint:errcheck
	conf := &Conf{}
	if err := viper.Unmarshal(conf); err != nil {
		return conf, fmt.Errorf("unable to unmarshal conf: %w", err)
	}

	return conf, nil
}
