package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/starwalkn/kono/sdk"
)

type Plugin struct{}

func NewPlugin() sdk.Plugin {
	return &Plugin{}
}

func (p *Plugin) Info() sdk.PluginInfo {
	return sdk.PluginInfo{
		Name:        "snakeify",
		Description: "The plugin can be used to transform JSON field names in the response into the snake_case style.",
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
		return fmt.Errorf("snakeify: cannot read body: %w", err)
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

	transformed := transformKeys(raw)

	newBody, err := json.Marshal(transformed)
	if err != nil {
		return fmt.Errorf("snakeify: cannot marshal JSON: %w", err)
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))

	return nil
}

func transformKeys(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{}, len(val))
		for k, child := range val {
			newMap[camelToSnake(k)] = transformKeys(child)
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

var (
	reUpper    = regexp.MustCompile("(.)([A-Z][a-z]+)")
	reUpperSeq = regexp.MustCompile("([a-z0-9])([A-Z])")
)

func camelToSnake(s string) string {
	s = reUpper.ReplaceAllString(s, "${1}_${2}")
	s = reUpperSeq.ReplaceAllString(s, "${1}_${2}")

	return strings.ToLower(s)
}
