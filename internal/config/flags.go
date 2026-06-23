package config

import (
	"github.com/spf13/cobra"
)

func AddLoggerFlags(c *cobra.Command) {
	c.PersistentFlags().String("log.level", "info", "one of debug, info, warn, error")
	c.PersistentFlags().String("log.format", "json", "one of json or text")
	c.PersistentFlags().Bool("log.source", false, "display the source file and line of the log call")
	c.PersistentFlags().String("log.color", "auto", "colorized output: auto, always, never (only applies to text format)")
}

func AddDaemonFlags(c *cobra.Command) {
	c.PersistentFlags().Int("daemon.concurrency", 2, "max number of concurrent backups")
	c.PersistentFlags().String("daemon.default_schedule", "", "default cron schedule for containers without buoy.schedule label")
	c.PersistentFlags().String("daemon.default_retention", "keep-within:7d,keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:3", "default retention policy for containers without buoy.retention label")
	c.PersistentFlags().String("daemon.resync_interval", "5m", "interval for periodic label resync (e.g., 5m, 1h, 0 to disable)")
	c.PersistentFlags().String("daemon.exec_timeout", "5m", "max time for docker exec hooks (e.g., 5m, 10m)")
	c.PersistentFlags().String("daemon.health_wait_timeout", "5m", "max time to wait for container health/dependency satisfaction (e.g., 5m)")
	c.PersistentFlags().String("daemon.backup_timeout", "1h", "max time for a backup cycle (e.g., 1h, 30m, 0 to disable)")
	c.PersistentFlags().String("daemon.check_schedule", "@weekly", "cron schedule for periodic restic check (empty = disabled)")
	c.PersistentFlags().String("daemon.db_path", "./buoy.db", "path to the bbolt state database")
	c.PersistentFlags().String("docker.host", "unix:///var/run/docker.sock", "Docker daemon socket path")
	c.PersistentFlags().String("restic.binary_path", "restic", "path to the restic binary")
	c.PersistentFlags().String("restic.compression", "auto", "restic compression mode (auto, off, max)")
	c.PersistentFlags().String("restic.password", "", "restic repository password")
	c.PersistentFlags().StringSlice("restic.repos", nil, "restic repository URLs (can be repeated)")
	c.PersistentFlags().Bool("api.enabled", true, "enable the HTTP API server")
	c.PersistentFlags().String("api.host", "0.0.0.0", "API listen host")
	c.PersistentFlags().Int("api.port", 8080, "API listen port")
	c.PersistentFlags().String("api.token", "", "API bearer token (empty = no auth)")
}

func AddNotifyFlags(c *cobra.Command) {
	c.PersistentFlags().StringSlice("notify.urls", nil, "shoutrrr notification URLs (can be repeated)")
	c.PersistentFlags().String("notify.level", "error", "notification level: none, error, all")
}

func AddConfigurationFlag(c *cobra.Command) {
	c.PersistentFlags().StringP("conf", "c", "", "configuration file to use")
}
