package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:           "buoy",
		Short:         "Backup Docker container volumes with restic",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return config.InitConfig(cmd)
		},
	}

	config.AddConfigurationFlag(root)
	config.AddLoggerFlags(root)
	config.AddDaemonFlags(root)
	config.AddNotifyFlags(root)

	root.AddCommand(RunCmd)
	root.AddCommand(version.NewCommand("buoy"))

	if err := root.Execute(); err != nil {
		slog.Error("unable to start", "error", err)
		os.Exit(1)
	}
}
