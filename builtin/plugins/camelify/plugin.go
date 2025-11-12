package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"strings"

	"github.com/starwalkn/tokka"
)

type Plugin struct {
	tokka.BasePlugin
}

func NewPlugin() tokka.Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string {
	return "camelify"
}

func (p *Plugin) Type() tokka.PluginType {
	return tokka.PluginTypeResponse
}

func (p *Plugin) Init(_ map[string]interface{}) {}

func (p *Plugin) Execute(ctx tokka.Context) {
	if ctx.Response() == nil || ctx.Response().Body == nil {
		return
	}

	var data map[string]any

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(ctx.Response().Body)
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		log.Printf("camelify: cannot unmarshal JSON: %v", err)
		return
	}

	newData := make(map[string]any)
	for k, v := range data {
		newKey := snakeToCamel(k)
		newData[newKey] = v
	}

	newBody, err := json.Marshal(newData)
	if err != nil {
		log.Printf("camelify: cannot marshal JSON: %v", err)
		return
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))
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
