package cmd

import (
	"github.com/spf13/cobra"
)

// addLoggerFlags adds support to configure the level of the logger.
func addLoggerFlags(c *cobra.Command) {
	c.PersistentFlags().String("log.level", "info", "one of debug, info, warn, error")
	c.PersistentFlags().String("log.format", "json", "one of json or text")
	c.PersistentFlags().Bool("log.source", false, "display the source file and line of the log call")
	c.PersistentFlags().String("log.color", "auto", "colorized output: auto, always, never (only applies to text format)")
}

// addDaemonFlags adds flags for the daemon configuration.
func addDaemonFlags(c *cobra.Command) {
	c.PersistentFlags().Int("daemon.concurrency", 1, "max number of concurrent backups")
	c.PersistentFlags().String("restic.password", "", "restic repository password")
	c.PersistentFlags().StringSlice("restic.repos", nil, "restic repository URLs (can be repeated)")
}

// addNotifyFlags adds flags for the notification configuration.
func addNotifyFlags(c *cobra.Command) {
	c.PersistentFlags().StringSlice("notify.urls", nil, "shoutrrr notification URLs (can be repeated)")
	c.PersistentFlags().String("notify.level", "error", "notification level: none, error, all")
}

// AddConfigurationFlag adds support to provide a configuration file on the
// command line.
func addConfigurationFlag(c *cobra.Command) {
	c.PersistentFlags().StringP("conf", "c", "", "configuration file to use")
}
