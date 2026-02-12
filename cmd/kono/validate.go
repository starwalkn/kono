package main

import (
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/xff16/kono"
)

var validateCmd = &cobra.Command{
	Use:          "validate",
	Short:        "Validates configuration file",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	Run: func(_ *cobra.Command, _ []string) {
		err := runValidate()
		if err != nil {
			log.Println(err)
			return
		}

		log.Println("configuration file is valid, you can start the server")
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
		cfgPath = "./kono.json"
	}

	_, err := kono.LoadConfig(cfgPath)
	if err != nil {
		return err
	}

	return nil
}
