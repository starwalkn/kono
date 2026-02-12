package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/starwalkn/kono"
)

type Plugin struct{}

func NewPlugin() kono.Plugin {
	return &Plugin{}
}

func (p *Plugin) Info() kono.PluginInfo {
	return kono.PluginInfo{
		Name:        "snakeify",
		Description: "The plugin can be used to transform JSON field names in the response into the snake_case style.",
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
		return fmt.Errorf("snakeify: cannot unmarshal JSON: %w", err)
	}

	newData := make(map[string]interface{})
	for k, v := range data {
		newKey := camelToSnake(k)
		newData[newKey] = v
	}

	newBody, err := json.Marshal(newData)
	if err != nil {
		return fmt.Errorf("snakeify: cannot marshal JSON: %w", err)
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))

	return nil
}

func camelToSnake(s string) string {
	re1 := regexp.MustCompile("(.)([A-Z][a-z]+)")
	re2 := regexp.MustCompile("([a-z0-9])([A-Z])")

	s = re1.ReplaceAllString(s, "${1}_${2}")
	s = re2.ReplaceAllString(s, "${1}_${2}")

	return strings.ToLower(s)
}
