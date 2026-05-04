package main

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
)

//go:embed templates/*.tmpl
var pluginTemplates embed.FS

type pluginInitFlags struct {
	pluginType  string
	name        string
	description string
	author      string
	output      string
}

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage Kono plugins",
}

func init() {
	rootCmd.AddCommand(pluginCmd)

	flags := &pluginInitFlags{}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a new plugin or middleware skeleton",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runPluginInit(*flags)
		},
	}

	initCmd.Flags().StringVar(&flags.pluginType, "type", "", "Plugin type: request, response, middleware")
	initCmd.Flags().StringVar(&flags.name, "name", "", "Plugin name (required)")
	initCmd.Flags().StringVar(&flags.description, "description", "", "Plugin description")
	initCmd.Flags().StringVar(&flags.author, "author", "", "Plugin author")
	initCmd.Flags().StringVar(&flags.output, "out", "", "Output file path (default: <name>.go in current directory)")

	_ = initCmd.MarkFlagRequired("type")
	_ = initCmd.MarkFlagRequired("name")

	pluginCmd.AddCommand(initCmd)
}

func runPluginInit(f pluginInitFlags) error {
	tmplName, err := templateNameForType(f.pluginType)
	if err != nil {
		return err
	}

	out := f.output
	if out == "" {
		out = f.name + ".go"
	}

	if _, err = os.Stat(out); err == nil {
		return fmt.Errorf("file already exists: %s", out)
	}

	tmpl, err := template.ParseFS(pluginTemplates, "templates/"+tmplName)
	if err != nil {
		return fmt.Errorf("load template: %w", err)
	}

	data := struct {
		Name        string
		Description string
		Author      string
	}{
		Name:        f.name,
		Description: f.description,
		Author:      f.author,
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format generated code: %w", err)
	}

	if err = os.MkdirAll(filepath.Dir(out), 0750); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if err = os.WriteFile(out, formatted, 0600); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "created %s\n", out)

	return nil
}

func templateNameForType(t string) (string, error) {
	switch t {
	case "request":
		return "request_plugin.go.tmpl", nil
	case "response":
		return "response_plugin.go.tmpl", nil
	case "middleware":
		return "middleware.go.tmpl", nil
	default:
		return "", fmt.Errorf("unknown plugin type: %q (must be request, response, middleware)", t)
	}
}
