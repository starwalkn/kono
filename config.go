package tokka

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

const (
	defaultUpstreamTimeout = 3 * time.Second
	defaultServerTimeout   = 5 * time.Second
)

type GatewayConfig struct {
	ConfigVersion string             `json:"config_version" yaml:"config_version" toml:"config_version"`
	Name          string             `json:"name" yaml:"name" toml:"name"`
	Version       string             `json:"version" yaml:"version" toml:"version"`
	Debug         bool               `json:"debug" yaml:"debug" toml:"debug"`
	Server        ServerConfig       `json:"server" yaml:"server" toml:"server"`
	Dashboard     DashboardConfig    `json:"dashboard" yaml:"dashboard" toml:"dashboard"`
	Features      []FeatureConfig    `json:"features" yaml:"features" toml:"features"`
	Middlewares   []MiddlewareConfig `json:"middlewares" yaml:"middlewares" toml:"middlewares"`
	Routes        []RouteConfig      `json:"routes" yaml:"routes" toml:"routes"`
}

type ServerConfig struct {
	Port    int           `json:"port" yaml:"port" toml:"port"`
	Timeout time.Duration `json:"timeout" yaml:"timeout" toml:"timeout"`
	Metrics MetricsConfig `json:"metrics" yaml:"metrics" toml:"metrics"`
}

type MetricsConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled" toml:"enabled"`
	Provider string `json:"provider" yaml:"provider" toml:"provider"`
}

type DashboardConfig struct {
	Enable  bool `json:"enable" yaml:"enable" toml:"enable"`
	Port    int  `json:"port" yaml:"port" toml:"port"`
	Timeout int  `json:"timeout" yaml:"timeout" toml:"timeout"`
}

type RouteConfig struct {
	Path                 string             `json:"path" yaml:"path" toml:"path"`
	Method               string             `json:"method" yaml:"method" toml:"method"`
	Plugins              []PluginConfig     `json:"plugins" yaml:"plugins" toml:"plugins"`
	Middlewares          []MiddlewareConfig `json:"middlewares" yaml:"middlewares" toml:"middlewares"`
	Upstreams            []UpstreamConfig   `json:"upstreams" yaml:"upstreams" toml:"upstreams"`
	Aggregation          AggregationConfig  `json:"aggregation" yaml:"aggregation" toml:"aggregation"`
	MaxParallelUpstreams int64              `json:"max_parallel_upstreams" yaml:"max_parallel_upstreams" toml:"max_parallel_upstreams"`
}

type AggregationConfig struct {
	Strategy            string `json:"strategy" yaml:"strategy" toml:"strategy"`
	AllowPartialResults bool   `json:"allow_partial_results" yaml:"allow_partial_results" toml:"allow_partial_results"`
}

type UpstreamConfig struct {
	URL                 string               `json:"url" yaml:"url" toml:"url"`
	Method              string               `json:"method" yaml:"method" toml:"method"`
	Timeout             time.Duration        `json:"timeout" yaml:"timeout" toml:"timeout"`
	Headers             map[string]string    `json:"headers" yaml:"headers" toml:"headers"`
	ForwardHeaders      []string             `json:"forward_headers" yaml:"forward_headers" toml:"forward_headers"`
	ForwardQueryStrings []string             `json:"forward_query_strings" yaml:"forward_query_strings" toml:"forward_query_strings"`
	Policy              UpstreamPolicyConfig `json:"policy" yaml:"policy" toml:"policy"`
}

type UpstreamPolicyConfig struct {
	AllowedStatuses     []int       `json:"allowed_status_codes" yaml:"allowed_status_codes" toml:"allowed_status_codes"`
	RequireBody         bool        `json:"allow_empty_body" yaml:"allow_empty_body" toml:"allow_empty_body"`
	MapStatusCodes      map[int]int `json:"map_status_codes" yaml:"map_status_codes" toml:"map_status_codes"`
	MaxResponseBodySize int64       `json:"max_response_body_size" yaml:"max_response_body_size" toml:"max_response_body_size"`

	RetryConfig          UpstreamRetryConfig          `json:"retry" yaml:"retry" toml:"retry"`
	CircuitBreakerConfig UpstreamCircuitBreakerConfig `json:"circuit_breaker" yaml:"circuit_breaker" toml:"circuit_breaker"`
}

type UpstreamRetryConfig struct {
	MaxRetries      int           `json:"max_retries" yaml:"max_retries" toml:"max_retries"`
	RetryOnStatuses []int         `json:"retry_on_statuses" yaml:"retry_on_statuses" toml:"retry_on_statuses"`
	BackoffDelay    time.Duration `json:"backoff_delay" yaml:"backoff_delay" toml:"backoff_delay"`
}

type UpstreamCircuitBreakerConfig struct {
	Enabled      bool          `json:"enabled" yaml:"enabled" toml:"enabled"`
	MaxFailures  int           `json:"max_failures" yaml:"max_failures" toml:"max_failures"`
	ResetTimeout time.Duration `json:"reset_timeout" yaml:"reset_timeout" toml:"reset_timeout"`
}

type PluginConfig struct {
	Name   string         `json:"name" yaml:"name" toml:"name"`
	Path   string         `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Config map[string]any `json:"config" yaml:"config" toml:"config"`
}

type MiddlewareConfig struct {
	Name          string         `json:"name" yaml:"name" toml:"name"`
	Path          string         `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Config        map[string]any `json:"config" yaml:"config" toml:"config"`
	CanFailOnLoad bool           `json:"can_fail_on_load" yaml:"can_fail_on_load" toml:"can_fail_on_load"`
	Override      bool           `json:"override" yaml:"override" toml:"override"`
}

type FeatureConfig struct {
	Name   string         `json:"name" yaml:"name" toml:"name"`
	Config map[string]any `json:"config" yaml:"config" toml:"config"`
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
	case ".toml":
		if err = toml.Unmarshal(data, &cfg); err != nil {
			log.Fatal("failed to parse toml: ", err)
		}
	default:
		log.Fatal("unknown config file extension:", filepath.Ext(path))
	}

	return ensureDefaults(cfg)
}

// ensureDefaults ensures that default values are used in required configuration fields if they are not explicitly set.
func ensureDefaults(cfg GatewayConfig) GatewayConfig {
	if cfg.Server.Timeout == 0 {
		cfg.Server.Timeout = defaultServerTimeout
	}

	for i := range cfg.Routes {
		if cfg.Routes[i].MaxParallelUpstreams < 1 {
			cfg.Routes[i].MaxParallelUpstreams = 2 * int64(runtime.NumCPU()) //nolint:mnd // shut up mnd
		}

		for j := range cfg.Routes[i].Upstreams {
			if cfg.Routes[i].Upstreams[j].Timeout == 0 {
				cfg.Routes[i].Upstreams[j].Timeout = defaultUpstreamTimeout
			}
		}
	}

	return cfg
}
