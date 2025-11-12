package snakeify

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"regexp"
	"strings"

	"github.com/starwalkn/bravka"
)

type Plugin struct {
	bravka.BasePlugin
}

func NewPlugin() bravka.Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string {
	return "snakeify"
}

func (p *Plugin) Type() bravka.PluginType {
	return bravka.PluginTypeResponse
}

func (p *Plugin) Init(_ map[string]interface{}) {}

func (p *Plugin) Execute(ctx bravka.Context) {
	if ctx.Response() == nil || ctx.Response().Body == nil {
		return
	}

	var data map[string]any

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(ctx.Response().Body)
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		log.Printf("snakeify: cannot unmarshal JSON: %v", err)
		return
	}

	newData := make(map[string]any)
	for k, v := range data {
		newKey := camelToSnake(k)
		newData[newKey] = v
	}

	newBody, err := json.Marshal(newData)
	if err != nil {
		log.Printf("snakeify: cannot marshal JSON: %v", err)
		return
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))
}

func camelToSnake(s string) string {
	re1 := regexp.MustCompile("(.)([A-Z][a-z]+)")
	re2 := regexp.MustCompile("([a-z0-9])([A-Z])")

	s = re1.ReplaceAllString(s, "${1}_${2}")
	s = re2.ReplaceAllString(s, "${1}_${2}")
	return strings.ToLower(s)
}
