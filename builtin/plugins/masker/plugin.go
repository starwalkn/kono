package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/starwalkn/kono/sdk"
)

const maskValue = "***"

type Plugin struct {
	fields map[string]struct{}
}

func NewPlugin() sdk.Plugin {
	return &Plugin{}
}

func (p *Plugin) Info() sdk.PluginInfo {
	return sdk.PluginInfo{
		Name:        "masker",
		Description: "Masks sensitive fields in JSON response body.",
		Version:     "v1",
		Author:      "starwalkn",
	}
}

func (p *Plugin) Type() sdk.PluginType {
	return sdk.PluginTypeResponse
}

func (p *Plugin) Init(cfg map[string]interface{}) error {
	p.fields = make(map[string]struct{})

	raw, ok := cfg["fields"].([]interface{})
	if !ok {
		return errors.New("fields must be an array")
	}

	for _, v := range raw {
		if field, vok := v.(string); vok {
			p.fields[field] = struct{}{}
		}
	}

	return nil
}

func (p *Plugin) Execute(ctx sdk.Context) error {
	if ctx.Response() == nil || ctx.Response().Body == nil {
		return nil
	}

	if len(p.fields) == 0 {
		return nil
	}

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(ctx.Response().Body); err != nil {
		return fmt.Errorf("masker: cannot read body: %w", err)
	}

	if buf.Len() == 0 {
		ctx.Response().Body = io.NopCloser(buf)
		return nil
	}

	var raw interface{}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		ctx.Response().Body = io.NopCloser(buf)
		return nil //nolint:nilerr // it is ok for plugins
	}

	masked := p.maskKeys(raw)

	newBody, err := json.Marshal(masked)
	if err != nil {
		return fmt.Errorf("masker: cannot marshal JSON: %w", err)
	}

	ctx.Response().Body = io.NopCloser(bytes.NewReader(newBody))

	return nil
}

func (p *Plugin) maskKeys(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, child := range val {
			if _, shouldMask := p.fields[k]; shouldMask {
				val[k] = maskValue
			} else {
				val[k] = p.maskKeys(child)
			}
		}

		return val
	case []interface{}:
		for i, item := range val {
			val[i] = p.maskKeys(item)
		}

		return val
	default:
		return val
	}
}
