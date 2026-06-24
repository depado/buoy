package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// InitConfig sets up viper and syncs values to cobra flags.
// Precedence: CLI flag > env var > config file > flag default.
func InitConfig(cmd *cobra.Command) error {
	viper.SetEnvPrefix("buoy")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

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

	// Sync viper values into flags that were not explicitly set on the
	// command line (config file or environment variable). Flags that were
	// changed on the CLI keep their user-supplied value.
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "conf" {
			return
		}
		if !f.Changed && viper.IsSet(f.Name) {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(viper.GetStringSlice(f.Name))
			} else {
				val := viper.Get(f.Name)
				_ = cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
			}
		}
	})

	// Push the resolved flag values into viper so NewConf() can unmarshal.
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "conf" {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			viper.Set(f.Name, sv.GetSlice())
		} else {
			viper.Set(f.Name, f.Value.String())
		}
	})

	return nil
}
