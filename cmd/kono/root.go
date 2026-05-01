package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/starwalkn/kono/internal/logger"
)

const fallbackConfigPath = "/etc/kono/config.yaml"

var cfgPath string

var rootCmd = &cobra.Command{
	Use:   "kono",
	Short: "Kono API Gateway",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log := logger.New(false)
		log.Error(err.Error())

		os.Exit(1)
	}
}

func init() {
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().StringVar(
		&cfgPath,
		"config",
		"",
		"Path to configuration file (env KONO_CONFIG)",
	)
}
