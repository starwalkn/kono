package kono

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

const (
	defaultUpstreamTimeout = 3 * time.Second
	defaultServerTimeout   = 5 * time.Second
)

type Config struct {
	Schema  string        `yaml:"schema" validate:"required,oneof=v1"`
	Debug   bool          `yaml:"debug"`
	Gateway GatewayConfig `yaml:"gateway" validate:"required"`
}

type GatewayConfig struct {
	Server  ServerConfig  `yaml:"server" validate:"required"`
	Routing RoutingConfig `yaml:"routing" validate:"required"`
}

type ServerConfig struct {
	Port    int           `yaml:"port" validate:"required,min=1,max=65535"`
	Timeout time.Duration `yaml:"timeout"`
	Metrics MetricsConfig `yaml:"metrics"`
}

type MetricsConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Provider string `yaml:"provider"`

	VictoriaMetrics VictoriaMetricsConfig `yaml:"victoria_metrics"`
}

type VictoriaMetricsConfig struct {
	Host     string        `yaml:"host"`
	Port     int           `yaml:"port"`
	Path     string        `yaml:"path"`
	Interval time.Duration `yaml:"interval"`
}

type RoutingConfig struct {
	RateLimiter RateLimiterConfig `yaml:"rate_limiter" validate:"omitempty"`
	Flows       []FlowConfig      `yaml:"flows" validate:"min=1,dive,required"`
}

type RateLimiterConfig struct {
	Enabled bool                   `yaml:"enabled"`
	Config  map[string]interface{} `yaml:"config" validate:"required"`
}

type FlowConfig struct {
	Path                 string             `yaml:"path" validate:"required"`
	Method               string             `yaml:"method" validate:"required"`
	Aggregation          AggregationConfig  `yaml:"aggregation" validate:"required"`
	MaxParallelUpstreams int64              `yaml:"max_parallel_upstreams"`
	Upstreams            []UpstreamConfig   `yaml:"upstreams" validate:"required,min=1,dive,required"`
	Plugins              []PluginConfig     `yaml:"plugins" validate:"omitempty,dive"`
	Middlewares          []MiddlewareConfig `yaml:"middlewares" validate:"omitempty,dive"`
}

type AggregationConfig struct {
	Strategy            string `yaml:"strategy" validate:"required,oneof=array merge"`
	AllowPartialResults bool   `yaml:"allow_partial_results"`
}

type UpstreamConfig struct {
	Name           string        `yaml:"name"`
	Hosts          AddrList      `yaml:"hosts" validate:"min=1,dive"`
	Path           string        `yaml:"path"`
	Method         string        `yaml:"method" validate:"required"`
	Timeout        time.Duration `yaml:"timeout"`
	ForwardHeaders []string      `yaml:"forward_headers"`
	ForwardQueries []string      `yaml:"forward_queries"`
	Policy         PolicyConfig  `yaml:"policy"`
}

type PluginConfig struct {
	Name   string                 `yaml:"name" validate:"required"`
	Source string                 `yaml:"source" validate:"required,oneof=builtin file"`
	Path   string                 `yaml:"path" validate:"required_if=Source file"`
	Config map[string]interface{} `yaml:"config"`
}

type MiddlewareConfig struct {
	Name   string                 `yaml:"name" validate:"required"`
	Source string                 `yaml:"source" validate:"required,oneof=builtin file"`
	Path   string                 `yaml:"path" validate:"required_if=Source file,omitempty"`
	Config map[string]interface{} `yaml:"config"`
}

type PolicyConfig struct {
	AllowedStatuses     []int       `yaml:"allowed_status_codes"`
	RequireBody         bool        `yaml:"allow_empty_body"`
	MapStatusCodes      map[int]int `yaml:"map_status_codes"`
	MaxResponseBodySize int64       `yaml:"max_response_body_size"`

	RetryConfig          RetryConfig          `yaml:"retry"`
	CircuitBreakerConfig CircuitBreakerConfig `yaml:"circuit_breaker"`
	LoadBalancingConfig  LoadBalancingConfig  `yaml:"load_balancer"`
}

type RetryConfig struct {
	MaxRetries      int           `yaml:"max_retries"`
	RetryOnStatuses []int         `yaml:"retry_on_statuses"`
	BackoffDelay    time.Duration `yaml:"backoff_delay"`
}

type CircuitBreakerConfig struct {
	Enabled      bool          `yaml:"enabled"`
	MaxFailures  int           `yaml:"max_failures"`
	ResetTimeout time.Duration `yaml:"reset_timeout"`
}

type LoadBalancingConfig struct {
	Mode string `yaml:"mode"`
}

type AddrList []string

func (a *AddrList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var addr string
		if err := value.Decode(&addr); err != nil {
			return err
		}

		*a = []string{addr}

		return nil
	case yaml.SequenceNode:
		var addrs []string
		if err := value.Decode(&addrs); err != nil {
			return err
		}

		*a = addrs

		return nil
	default:
		return fmt.Errorf("unexpected YAML node kind for AddrList: %v", value.Kind)
	}
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read configuration file: %w", err)
	}

	var cfg Config

	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("cannot parse configuration file: %w", err)
	}

	ensureGatewayDefaults(&cfg.Gateway)

	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := fld.Tag.Get("yaml")
		if name == "" || name == "-" {
			return strings.ToLower(fld.Name)
		}

		return strings.ToLower(strings.Split(name, ",")[0])
	})

	if err = v.Struct(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: \n%w", formatValidationError(err))
	}

	return cfg, nil
}

// ensureGatewayDefaults ensures that default values are used in required configuration fields if they are not explicitly set.
func ensureGatewayDefaults(cfg *GatewayConfig) {
	if cfg.Server.Timeout == 0 {
		cfg.Server.Timeout = defaultServerTimeout
	}

	for i := range cfg.Routing.Flows {
		if cfg.Routing.Flows[i].MaxParallelUpstreams < 1 {
			cfg.Routing.Flows[i].MaxParallelUpstreams = int64(2 * runtime.NumCPU()) //nolint:mnd // shut up mnt
		}

		for j := range cfg.Routing.Flows[i].Upstreams {
			if cfg.Routing.Flows[i].Upstreams[j].Timeout == 0 {
				cfg.Routing.Flows[i].Upstreams[j].Timeout = defaultUpstreamTimeout
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
			"  %s: %s",
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

	case "required_if":
		if fe.Field() == "path" {
			return "is required when source is 'file'"
		}

		return fe.Error()
	default:
		return fmt.Sprintf("validation failed on '%s'", fe.Tag())
	}
}
