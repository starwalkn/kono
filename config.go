package kono

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

const (
	defaultUpstreamTimeout = 3 * time.Second
	defaultServerTimeout   = 5 * time.Second
)

type Config struct {
	ConfigVersion     string             `json:"config_version" yaml:"config_version" toml:"config_version" validate:"required,oneof=v1"`
	Name              string             `json:"name" yaml:"name" toml:"name" validate:"required"`
	Version           string             `json:"version" yaml:"version" toml:"version" validate:"required"`
	Debug             bool               `json:"debug" yaml:"debug" toml:"debug"`
	Server            ServerConfig       `json:"server" yaml:"server" toml:"server"`
	GlobalMiddlewares []MiddlewareConfig `json:"middlewares" yaml:"middlewares" toml:"middlewares"`
	Router            RouterConfig       `json:"router" yaml:"router" toml:"router" validate:"required"`
}

type ServerConfig struct {
	Port    int           `json:"port" yaml:"port" toml:"port" validate:"required,min=1,max=65535"`
	Timeout time.Duration `json:"timeout" yaml:"timeout" toml:"timeout"`
	Metrics MetricsConfig `json:"metrics" yaml:"metrics" toml:"metrics"`
}

type MetricsConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled" toml:"enabled"`
	Provider string `json:"provider" yaml:"provider" toml:"provider"`

	VictoriaMetrics VictoriaMetricsConfig `json:"victoria_metrics" yaml:"victoria_metrics" toml:"victoria_metrics"`
}

type VictoriaMetricsConfig struct {
	Host     string        `json:"host" yaml:"host" toml:"host"`
	Port     int           `json:"port" yaml:"port" toml:"port"`
	Path     string        `json:"path" yaml:"path" toml:"path"`
	Interval time.Duration `json:"interval" yaml:"interval" toml:"interval"`
}

type RouterConfig struct {
	RateLimiter RateLimiterConfig `json:"rate_limiter" yaml:"rate_limiter" toml:"rate_limiter"`
	Routes      []RouteConfig     `json:"routes" yaml:"routes" toml:"routes" validate:"min=1,dive"`
}

type RateLimiterConfig struct {
	Enabled bool                   `json:"enabled" yaml:"enabled" toml:"enabled"`
	Config  map[string]interface{} `json:"config" yaml:"config" toml:"config"`
}

type RouteConfig struct {
	Path                 string             `json:"path" yaml:"path" toml:"path" validate:"required"`
	Method               string             `json:"method" yaml:"method" toml:"method" validate:"required"`
	Plugins              []PluginConfig     `json:"plugins" yaml:"plugins" toml:"plugins"`
	Middlewares          []MiddlewareConfig `json:"middlewares" yaml:"middlewares" toml:"middlewares"`
	Upstreams            []UpstreamConfig   `json:"upstreams" yaml:"upstreams" toml:"upstreams" validate:"required,min=1,dive"`
	Aggregation          AggregationConfig  `json:"aggregation" yaml:"aggregation" toml:"aggregation"`
	MaxParallelUpstreams int64              `json:"max_parallel_upstreams" yaml:"max_parallel_upstreams" toml:"max_parallel_upstreams"`
}

type PluginConfig struct {
	Name   string                 `json:"name" yaml:"name" toml:"name"`
	Path   string                 `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Config map[string]interface{} `json:"config" yaml:"config" toml:"config"`
}

type MiddlewareConfig struct {
	Name          string                 `json:"name" yaml:"name" toml:"name"`
	Path          string                 `json:"path,omitempty" yaml:"path,omitempty" toml:"path,omitempty"`
	Config        map[string]interface{} `json:"config" yaml:"config" toml:"config"`
	CanFailOnLoad bool                   `json:"can_fail_on_load" yaml:"can_fail_on_load" toml:"can_fail_on_load"`
	Override      bool                   `json:"override" yaml:"override" toml:"override"`
}

type UpstreamConfig struct {
	Name                string        `json:"name" yaml:"name" toml:"name"`
	Hosts               []string      `json:"hosts" yaml:"hosts" toml:"hosts" validate:"min=1,dive"`
	Method              string        `json:"method" yaml:"method" toml:"method" validate:"required"`
	Timeout             time.Duration `json:"timeout" yaml:"timeout" toml:"timeout"`
	ForwardHeaders      []string      `json:"forward_headers" yaml:"forward_headers" toml:"forward_headers"`
	ForwardQueryStrings []string      `json:"forward_query_strings" yaml:"forward_query_strings" toml:"forward_query_strings"`
	Policy              PolicyConfig  `json:"policy" yaml:"policy" toml:"policy"`
}

type PolicyConfig struct {
	AllowedStatuses     []int       `json:"allowed_status_codes" yaml:"allowed_status_codes" toml:"allowed_status_codes"`
	RequireBody         bool        `json:"allow_empty_body" yaml:"allow_empty_body" toml:"allow_empty_body"`
	MapStatusCodes      map[int]int `json:"map_status_codes" yaml:"map_status_codes" toml:"map_status_codes"`
	MaxResponseBodySize int64       `json:"max_response_body_size" yaml:"max_response_body_size" toml:"max_response_body_size"`

	RetryConfig          RetryConfig          `json:"retry" yaml:"retry" toml:"retry"`
	CircuitBreakerConfig CircuitBreakerConfig `json:"circuit_breaker" yaml:"circuit_breaker" toml:"circuit_breaker"`
	LoadBalancerConfig   LoadBalancerConfig   `json:"load_balancer" yaml:"load_balancer" toml:"load_balancer"`
}

type RetryConfig struct {
	MaxRetries      int           `json:"max_retries" yaml:"max_retries" toml:"max_retries"`
	RetryOnStatuses []int         `json:"retry_on_statuses" yaml:"retry_on_statuses" toml:"retry_on_statuses"`
	BackoffDelay    time.Duration `json:"backoff_delay" yaml:"backoff_delay" toml:"backoff_delay"`
}

type CircuitBreakerConfig struct {
	Enabled      bool          `json:"enabled" yaml:"enabled" toml:"enabled"`
	MaxFailures  int           `json:"max_failures" yaml:"max_failures" toml:"max_failures"`
	ResetTimeout time.Duration `json:"reset_timeout" yaml:"reset_timeout" toml:"reset_timeout"`
}

type LoadBalancerConfig struct {
	Mode string `json:"mode" yaml:"mode" toml:"mode"`
}

type AggregationConfig struct {
	Strategy            string `json:"strategy" yaml:"strategy" toml:"strategy" validate:"required,oneof=array merge"`
	AllowPartialResults bool   `json:"allow_partial_results" yaml:"allow_partial_results" toml:"allow_partial_results"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read configuration file: %w", err)
	}

	var cfg Config

	switch filepath.Ext(path) {
	case ".json":
		if err = json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("cannot parse configuration file: %w", err)
		}
	case ".yaml", ".yml":
		if err = yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("cannot parse configuration file: %w", err)
		}
	case ".toml":
		if err = toml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("cannot parse configuration file: %w", err)
		}
	default:
		return Config{}, fmt.Errorf("unknown configuration file extension: %s", filepath.Ext(path))
	}

	ensureDefaults(&cfg)

	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := fld.Tag.Get(strings.TrimPrefix(filepath.Ext(path), "."))
		if name == "" || name == "-" {
			return strings.ToLower(fld.Name)
		}

		return strings.ToLower(strings.Split(name, ",")[0])
	})

	if err = v.Struct(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", formatValidationError(err))
	}

	return cfg, nil
}

// ensureDefaults ensures that default values are used in required configuration fields if they are not explicitly set.
func ensureDefaults(cfg *Config) {
	if cfg.Server.Timeout == 0 {
		cfg.Server.Timeout = defaultServerTimeout
	}

	for i := range cfg.Router.Routes {
		if cfg.Router.Routes[i].MaxParallelUpstreams < 1 {
			cfg.Router.Routes[i].MaxParallelUpstreams = int64(2 * runtime.NumCPU()) //nolint:mnd // shut up mnt
		}

		for j := range cfg.Router.Routes[i].Upstreams {
			if cfg.Router.Routes[i].Upstreams[j].Timeout == 0 {
				cfg.Router.Routes[i].Upstreams[j].Timeout = defaultUpstreamTimeout
			}
		}
	}
}

func formatValidationError(err error) error {
	var ves validator.ValidationErrors

	if ok := errors.As(err, &ves); !ok {
		return err
	}

	var messages []string

	for _, fe := range ves {
		path := strings.TrimPrefix(fe.Namespace(), "Config.")

		messages = append(messages, fmt.Sprintf(
			"%s: %s",
			path,
			humanMessage(fe),
		))
	}

	return errors.New(strings.Join(messages, "\n"))
}

func humanMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "field is required"

	case "min":
		return fmt.Sprintf("must have at least %s item(s)", fe.Param())

	case "oneof":
		return fmt.Sprintf("must be one of [%s]", fe.Param())

	case "hosts":
		return "must be a valid URL"

	default:
		return fmt.Sprintf("validation failed on '%s'", fe.Tag())
	}
}
