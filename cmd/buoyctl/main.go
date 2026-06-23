package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:              "buoyctl",
		Short:            "Manage a running buoy daemon",
		Version:          version.Version,
		SilenceUsage:     true,
		SilenceErrors:    true,
		PersistentPreRunE: initBuoyctl,
	}

	root.PersistentFlags().String("api.url", "http://127.0.0.1:8080", "buoy API URL (env: BUOY_URL, BUOY_API_URL)")
	root.PersistentFlags().String("api.token", "", "buoy API bearer token (env: BUOY_TOKEN, BUOY_API_TOKEN)")
	root.PersistentFlags().StringP("conf", "c", "", "configuration file to use")

	viper.BindEnv("api.url", "BUOY_URL")
	viper.BindEnv("api.token", "BUOY_TOKEN")

	setupRepoCommands()
	root.AddCommand(repoCmd)
	setupDiscoverCommand()
	root.AddCommand(discoverCmd)

	root.AddCommand(version.NewCommand("buoyctl"))

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func initBuoyctl(cmd *cobra.Command, args []string) error {
	if cmd.Name() == "version" {
		return nil
	}
	return config.InitConfig(cmd)
}
