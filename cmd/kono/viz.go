package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/starwalkn/kono"
)

var vizCmd = &cobra.Command{
	Use:   "viz",
	Short: "Visualize flows of the gateway configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfgPath == "" {
			cfgPath = os.Getenv("KONO_CONFIG")
		}
		if cfgPath == "" {
			cfgPath = "./kono.yaml"
		}

		cfg, err := kono.LoadConfig(cfgPath)
		if err != nil {
			return err
		}

		return VisualizeFlowsTree(cfg.Gateway.Routing.Flows)
	},
}

func init() {
	rootCmd.AddCommand(vizCmd)
}

const (
	colorReset   = "\033[0m"
	colorBlue    = "\033[1;34m"
	colorGreen   = "\033[1;32m"
	colorYellow  = "\033[1;33m"
	colorMagenta = "\033[1;35m"
	colorCyan    = "\033[1;36m"
)

func VisualizeFlowsTree(flows []kono.FlowConfig) error {
	for fi, f := range flows {
		flowPrefix := "├── "
		if fi == len(flows)-1 {
			flowPrefix = "└── "
		}

		fmt.Println(flowPrefix + colorBlue + f.Method + " " + f.Path + colorReset)

		if len(f.Plugins) > 0 {
			var pluginNames []string
			for _, p := range f.Plugins {
				pluginNames = append(pluginNames, p.Name)
			}

			fmt.Println("│   └── \u001B[1;35mplugins: [" + colorMagenta + strings.Join(pluginNames, ", ") + "]" + colorReset)
		}

		if len(f.Middlewares) > 0 {
			var mwNames []string
			for _, m := range f.Middlewares {
				mwNames = append(mwNames, m.Name)
			}

			fmt.Println("│   └── \u001B[1;36mmiddlewares: [" + colorCyan + strings.Join(mwNames, ", ") + "]" + colorReset)
		}

		for ui, u := range f.Upstreams {
			upPrefix := "│   ├── "
			lastUpstream := ui == len(f.Upstreams)-1
			if lastUpstream {
				upPrefix = "│   └── "
			}
			upLabel := u.Name
			if upLabel == "" {
				upLabel = "upstream"
			}

			fmt.Println(upPrefix + colorGreen + upLabel + colorReset)

			for hi, host := range u.Hosts {
				hostPrefix := "│       ├── "
				if hi == len(u.Hosts)-1 {
					hostPrefix = "│       └── "
				}

				fmt.Println(hostPrefix + colorYellow + host + colorReset)
			}
		}

		fmt.Println()
	}

	return nil
}
