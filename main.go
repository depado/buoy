package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/depado/buoy/cmd"
)

// Main function that will be executed from the root command.
func run() {
	conf, err := cmd.NewConf()
	if err != nil {
		slog.Error("unable to load configuration", "error", err)
		os.Exit(1)
	}

	lg := cmd.NewLogger(conf)
	slog.SetDefault(lg)
	lg.Info("starting", "version", cmd.Version, "build", cmd.Build, "date", cmd.BuildDate)
}

func main() {
	root := &cobra.Command{
		Use:           "buoy",
		Short:         "Simple go project",
		Version:       cmd.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			run()
		},
	}

	cmd.Setup(root)

	// Run the command
	if err := root.Execute(); err != nil {
		slog.Error("unable to start", "error", err)
		os.Exit(1)
	}
}
