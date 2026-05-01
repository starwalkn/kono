package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/starwalkn/kono/sdk"
)

type Plugin struct{}

func NewPlugin() sdk.Plugin {
	return &Plugin{}
}

func (p *Plugin) Info() sdk.PluginInfo {
	return sdk.PluginInfo{
		Name:        "camelify",
		Description: "The plugin can be used to transform JSON field names in the response into the camelCase style.",
		Version:     "v1",
		Author:      "starwalkn",
	}
}

func (p *Plugin) Type() sdk.PluginType {
	return sdk.PluginTypeResponse
}

func (p *Plugin) Init(_ map[string]interface{}) error { return nil }

func (p *Plugin) Execute(ctx sdk.Context) error {
	if ctx.Response() == nil || ctx.Response().Body == nil {
		return nil
	}

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(ctx.Response().Body); err != nil {
		return fmt.Errorf("camelify: cannot read body: %w", err)
	}

	if buf.Len() == 0 {
		ctx.Response().Body = io.NopCloser(buf)
		return nil
	}

	var raw interface{}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		ctx.Response().Body = io.NopCloser(buf)
		return nil //nolint:nilerr // normal behaviour
	}

	newBody, err := json.Marshal(transformKeys(raw))
	if err != nil {
		return fmt.Errorf("camelify: cannot marshal JSON: %w", err)
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))

	return nil
}

func transformKeys(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{}, len(val))
		for k, child := range val {
			newMap[snakeToCamel(k)] = transformKeys(child)
		}

		return newMap
	case []interface{}:
		newSlice := make([]interface{}, len(val))
		for i, item := range val {
			newSlice[i] = transformKeys(item)
		}

		return newSlice
	default:
		return val
	}
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
