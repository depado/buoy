package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// InitConfig sets up viper with environment variables, config file, and applies
// viper values to cobra flags that were not explicitly set on the command line.
// This ensures the correct precedence: CLI flag > env var > config file > flag default.
func InitConfig(cmd *cobra.Command) error {
	viper.SetEnvPrefix("buoy")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "conf" {
			return
		}
		s := f.Value.String()
		if s != "" && s != "0" && s != "false" && s != "[]" && s != "0s" {
			viper.SetDefault(f.Name, s)
		}
	})

	confFile, _ := cmd.Flags().GetString("conf")
	if confFile != "" {
		viper.SetConfigFile(confFile)
	} else {
		viper.SetConfigName("conf")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/config/")
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("unable to read config file: %w", err)
		}
	}

	viper.AutomaticEnv()

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "conf" && f.Changed {
			viper.Set(f.Name, f.Value.String())
		}
	})

	return nil
}
