package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Setup takes the root command, binds the flags to it and adds the other
// commands.
func Setup(root *cobra.Command) {
	addConfigurationFlag(root)
	addLoggerFlags(root)
	addDaemonFlags(root)
	addNotifyFlags(root)

	if err := viper.BindPFlags(root.PersistentFlags()); err != nil {
		slog.Error("unable to bind flags", "error", err)
		os.Exit(1)
	}

	root.AddCommand(versionCmd)
	root.AddCommand(runCmd)

}
