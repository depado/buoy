package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func InitConfig(cmd *cobra.Command, v *viper.Viper) error {
	v.SetEnvPrefix("buoy")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	confFile, _ := cmd.Flags().GetString("conf")
	if confFile != "" {
		v.SetConfigFile(confFile)
	} else {
		v.SetConfigName("conf")
		v.AddConfigPath(".")
		v.AddConfigPath("/config/")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("unable to read config file: %w", err)
		}
	}

	v.AutomaticEnv()

	return v.BindPFlags(cmd.Flags())
}
