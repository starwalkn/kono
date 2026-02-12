package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/starwalkn/kono"
)

type Plugin struct{}

func NewPlugin() kono.Plugin {
	return &Plugin{}
}

func (p *Plugin) Info() kono.PluginInfo {
	return kono.PluginInfo{
		Name:        "camelify",
		Description: "The plugin can be used to transform JSON field names in the response into the camelCase style.",
		Version:     "v1",
		Author:      "starwalkn",
	}
}

func (p *Plugin) Type() kono.PluginType {
	return kono.PluginTypeResponse
}

func (p *Plugin) Init(_ map[string]interface{}) {}

func (p *Plugin) Execute(ctx kono.Context) error {
	if ctx.Response() == nil || ctx.Response().Body == nil {
		return nil
	}

	var data map[string]interface{}

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(ctx.Response().Body)
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		return fmt.Errorf("camelify: cannot unmarshal JSON: %w", err)
	}

	newData := make(map[string]interface{})
	for k, v := range data {
		newKey := snakeToCamel(k)
		newData[newKey] = v
	}

	newBody, err := json.Marshal(newData)
	if err != nil {
		return fmt.Errorf("camelify: cannot marshal JSON: %w", err)
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))

	return nil
}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")

	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}

	return strings.Join(parts, "")
}
