package tokka

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type GatewayConfig struct {
	Schema      string             `json:"schema" yaml:"schema"`
	Name        string             `json:"name" yaml:"name"`
	Version     string             `json:"version" yaml:"version"`
	Debug       bool               `json:"debug" yaml:"debug"`
	Server      ServerConfig       `json:"server" yaml:"server"`
	Dashboard   DashboardConfig    `json:"dashboard" yaml:"dashboard"`
	Plugins     []CorePluginConfig `json:"plugins" yaml:"plugins"`
	Middlewares []MiddlewareConfig `json:"middlewares" yaml:"middlewares"`
	Routes      []RouteConfig      `json:"routes" yaml:"routes"`
}

type ServerConfig struct {
	Port    int `json:"port" yaml:"port"`
	Timeout int `json:"timeout" yaml:"timeout"`
}

type DashboardConfig struct {
	Enable  bool `json:"enable" yaml:"enable"`
	Port    int  `json:"port" yaml:"port"`
	Timeout int  `json:"timeout" yaml:"timeout"`
}

type RouteConfig struct {
	Path                string             `json:"path" yaml:"path"`
	Method              string             `json:"method" yaml:"method"`
	Plugins             []PluginConfig     `json:"plugins" yaml:"plugins"`
	Middlewares         []MiddlewareConfig `json:"middlewares" yaml:"middlewares"`
	Backends            []BackendConfig    `json:"backends" yaml:"backends"`
	Aggregate           string             `json:"aggregate" yaml:"aggregate"`
	Transform           string             `json:"transform" yaml:"transform"`
	AllowPartialResults bool               `json:"allow_partial_results" yaml:"allow_partial_results"`
}

type BackendConfig struct {
	URL                 string            `json:"url" yaml:"url"`
	Method              string            `json:"method" yaml:"method"`
	Timeout             int64             `json:"timeout" yaml:"timeout"`
	Headers             map[string]string `json:"headers" yaml:"headers"`
	ForwardHeaders      []string          `json:"forward_headers" yaml:"forward_headers"`
	ForwardQueryStrings []string          `json:"forward_query_strings" yaml:"forward_query_strings"`
}

type PluginConfig struct {
	Name   string                 `json:"name" yaml:"name"`
	Path   string                 `json:"path,omitempty" yaml:"path,omitempty"`
	Config map[string]interface{} `json:"config" yaml:"config"`
}

type MiddlewareConfig struct {
	Name          string                 `json:"name" yaml:"name"`
	Path          string                 `json:"path,omitempty" yaml:"path,omitempty"`
	Config        map[string]interface{} `json:"config" yaml:"config"`
	CanFailOnLoad bool                   `json:"can_fail_on_load" yaml:"can_fail_on_load"`
	Override      bool                   `json:"override" yaml:"override"`
}

type CorePluginConfig struct {
	Name   string                 `json:"name" yaml:"name"`
	Config map[string]interface{} `json:"config" yaml:"config"`
}

func LoadConfig(path string) GatewayConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("failed to read config file:", err)
	}

	var cfg GatewayConfig

	switch filepath.Ext(path) {
	case ".json":
		if err = json.Unmarshal(data, &cfg); err != nil {
			log.Fatal("failed to parse json:", err)
		}
	case ".yaml", ".yml":
		if err = yaml.Unmarshal(data, &cfg); err != nil {
			log.Fatal("failed to parse yaml:", err)
		}
	default:
		log.Fatal("unknown config file extension:", filepath.Ext(path))
	}

	// Loading core-level plugins.
	for _, pcfg := range cfg.Plugins {
		p := createCorePlugin(pcfg.Name)
		if p == nil {
			log.Println("failed to create core plugin:", pcfg.Name)
			continue
		}

		if err = p.Init(pcfg.Config); err != nil {
			log.Fatal("failed to init core plugin:", err)
		}

		if err = p.Start(); err != nil {
			log.Fatal("failed to start core plugin:", err)
		}

		registerActiveCorePlugin(pcfg.Name, p)
	}

	return cfg
}
