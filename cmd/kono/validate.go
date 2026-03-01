package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/starwalkn/kono"
)

var validateCmd = &cobra.Command{
	Use:          "validate",
	Short:        "Validates configuration file",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	Run: func(_ *cobra.Command, _ []string) {
		err := runValidate()
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return
		}

		fmt.Println("OK")
		os.Exit(1)
	},
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func runValidate() error {
	if cfgPath == "" {
		cfgPath = os.Getenv("KONO_CONFIG")
	}
	if cfgPath == "" {
		cfgPath = fallbackConfigPath
	}

	_, err := kono.LoadConfig(cfgPath)
	if err != nil {
		return err
	}

	return nil
}
