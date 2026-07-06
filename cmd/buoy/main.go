package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/version"
)

var conf config.Conf

func main() {
	root := &cobra.Command{
		Use:           "buoy",
		Short:         "Backup Docker container volumes with restic",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			v := viper.New()
			if err := config.InitConfig(cmd, v); err != nil {
				return err
			}
			return v.Unmarshal(&conf)
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
