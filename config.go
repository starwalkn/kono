package kono

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/creasty/defaults"
	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// parallelismMultiplier scales ParallelUpstreams relative to runtime.NumCPU()
// when no explicit value is configured.
const parallelismMultiplier = 2

type Config struct {
	Schema  string        `yaml:"schema" validate:"required,oneof=v1"`
	Debug   bool          `yaml:"debug"`
	Gateway GatewayConfig `yaml:"gateway" validate:"required"`
}

type GatewayConfig struct {
	Service ServiceConfig `yaml:"service"`
	Server  ServerConfig  `yaml:"server"  validate:"required"`
	Routing RoutingConfig `yaml:"routing" validate:"required"`
}

type ServiceConfig struct {
	Name string `yaml:"name" default:"kono"`
}

type ServerConfig struct {
	Port    int           `yaml:"port"    validate:"required,min=1,max=65535"`
	Timeout time.Duration `yaml:"timeout" default:"5s"`
	Pprof   PprofConfig   `yaml:"pprof"`
	Metrics MetricsConfig `yaml:"metrics"`
	Tracing TracingConfig `yaml:"tracing"`
}

type PprofConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port" validate:"required_if=Enabled true,omitempty,min=1,max=65535"`
}

type MetricsConfig struct {
	Enabled  bool       `yaml:"enabled"`
	Exporter string     `yaml:"exporter" validate:"required_if=Enabled true,omitempty,oneof=otlp prometheus"`
	OTLP     OTLPConfig `yaml:"otlp"`
}

type TracingConfig struct {
	Enabled       bool       `yaml:"enabled"`
	Exporter      string     `yaml:"exporter" validate:"required_if=Enabled true,omitempty,oneof=otlp"`
	SamplingRatio float64    `yaml:"sampling_ratio" default:"1.0" validate:"min=0,max=1"`
	OTLP          OTLPConfig `yaml:"otlp"`
}

type OTLPConfig struct {
	Endpoint string        `yaml:"endpoint"`
	Insecure bool          `yaml:"insecure"`
	Interval time.Duration `yaml:"interval"`
}

type RoutingConfig struct {
	RateLimiter    RateLimiterConfig `yaml:"rate_limiter" validate:"omitempty"`
	TrustedProxies []string          `yaml:"trusted_proxies"`
	Flows          []FlowConfig      `yaml:"flows" validate:"min=1,dive,required"`
}

type RateLimiterConfig struct {
	Enabled bool                   `yaml:"enabled"`
	Config  map[string]interface{} `yaml:"config" validate:"required"`
}

type FlowConfig struct {
	Path        string `yaml:"path"   validate:"required,startswith=/"`
	Method      string `yaml:"method" validate:"required,oneof=GET POST PUT PATCH DELETE HEAD OPTIONS"`
	Passthrough bool   `yaml:"passthrough"`

	// ParallelUpstreams defaults to 2×NumCPU when unset or zero.
	ParallelUpstreams int64 `yaml:"parallel_upstreams"`

	Aggregation *AggregationConfig `yaml:"aggregation"  validate:"required_if=Passthrough false"`
	Upstreams   []UpstreamConfig   `yaml:"upstreams"    validate:"required,min=1,dive,required"`
	Plugins     []PluginConfig     `yaml:"plugins"      validate:"omitempty,dive"`
	Middlewares []MiddlewareConfig `yaml:"middlewares"  validate:"omitempty,dive"`
}

type AggregationConfig struct {
	BestEffort bool              `yaml:"best_effort"`
	Strategy   string            `yaml:"strategy"    validate:"required,oneof=array merge namespace"`
	OnConflict *OnConflictConfig `yaml:"on_conflict" validate:"required_if=Strategy merge"`
}

type OnConflictConfig struct {
	Policy   string `yaml:"policy"          validate:"oneof=overwrite error first prefer"`
	Upstream string `yaml:"prefer_upstream" validate:"required_if=Policy prefer"`
}

type UpstreamConfig struct {
	Name    string        `yaml:"name" validate:"required"`
	Hosts   AddrList      `yaml:"hosts" validate:"min=1,dive"`
	Path    string        `yaml:"path"`
	Method  string        `yaml:"method"`
	Timeout time.Duration `yaml:"timeout" default:"3s"`

	ForwardHeaders []string `yaml:"forward_headers"`
	ForwardQueries []string `yaml:"forward_queries"`
	ForwardParams  []string `yaml:"forward_params"`

	Policy    PolicyConfig    `yaml:"policy"`
	Transport TransportConfig `yaml:"transport"`
}

type TransportConfig struct {
	MaxIdleConns        int           `yaml:"max_idle_conns"         default:"100"`
	MaxIdleConnsPerHost int           `yaml:"max_idle_conns_per_host" default:"50"`
	IdleConnTimeout     time.Duration `yaml:"idle_conn_timeout"      default:"90s"`
}

type PluginConfig struct {
	Name   string                 `yaml:"name"   validate:"required"`
	Source string                 `yaml:"source" validate:"required,oneof=builtin file"`
	Path   string                 `yaml:"path"   validate:"required_if=Source file"`
	Config map[string]interface{} `yaml:"config"`
}

type MiddlewareConfig struct {
	Name   string                 `yaml:"name"   validate:"required"`
	Source string                 `yaml:"source" validate:"required,oneof=builtin file"`
	Path   string                 `yaml:"path"   validate:"required_if=Source file,omitempty"`
	Config map[string]interface{} `yaml:"config"`
}

type PolicyConfig struct {
	HeaderBlacklist     []string `yaml:"header_blacklist"`
	AllowedStatuses     []int    `yaml:"allowed_statuses"`
	RequireBody         bool     `yaml:"require_body"`
	MaxResponseBodySize int64    `yaml:"max_response_body_size"`

	RetryConfig          RetryConfig          `yaml:"retry"`
	CircuitBreakerConfig CircuitBreakerConfig `yaml:"circuit_breaker"`
	LoadBalancingConfig  LoadBalancingConfig  `yaml:"load_balancing"`
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
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
		return fmt.Errorf("expects a string or sequence, got %v", value.Kind)
	default:
		return fmt.Errorf("unexpected yaml node kind for AddrList: %v", value.Kind)
	}
}

// LoadConfig reads, parses, applies defaults, validates, and returns the config.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read configuration file: %w", err)
	}

	var cfg Config

	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("cannot parse configuration file: %w", err)
	}

	if err = defaults.Set(&cfg); err != nil {
		return Config{}, fmt.Errorf("cannot apply configuration defaults: %w", err)
	}

	applyDynamicDefaults(&cfg)

	v := newValidator()

	if err = v.Struct(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", formatValidationError(err))
	}

	if err = validatePathParams(cfg); err != nil {
		return Config{}, fmt.Errorf("invalid path params configuration: %w", err)
	}

	return cfg, nil
}

// applyDynamicDefaults sets defaults that cannot be expressed as static tag values
// because they depend on runtime state (e.g. NumCPU).
func applyDynamicDefaults(cfg *Config) {
	for i := range cfg.Gateway.Routing.Flows {
		f := &cfg.Gateway.Routing.Flows[i]
		if f.ParallelUpstreams < 1 {
			f.ParallelUpstreams = int64(parallelismMultiplier * runtime.NumCPU())
		}
	}
}

func newValidator() *validator.Validate {
	v := validator.New()

	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := fld.Tag.Get("yaml")
		if name == "" || name == "-" {
			return strings.ToLower(fld.Name)
		}

		return strings.ToLower(strings.Split(name, ",")[0])
	})

	return v
}

var pathParamPattern = regexp.MustCompile(`\{([^}]+)\}`)

func validatePathParams(cfg Config) error {
	for _, f := range cfg.Gateway.Routing.Flows {
		flowParams := extractPathParams(f.Path)

		for _, u := range f.Upstreams {
			if err := validateUpstreamParams(u, flowParams, f.Path); err != nil {
				return err
			}
		}
	}

	return nil
}

func extractPathParams(path string) map[string]struct{} {
	params := make(map[string]struct{})

	for _, match := range pathParamPattern.FindAllStringSubmatch(path, -1) {
		params[match[1]] = struct{}{}
	}

	return params
}

func validateUpstreamParams(u UpstreamConfig, flowParams map[string]struct{}, flowPath string) error {
	for _, match := range pathParamPattern.FindAllStringSubmatch(u.Path, -1) {
		if _, ok := flowParams[match[1]]; !ok {
			return fmt.Errorf(
				"upstream %q: path param '{%s}' not declared in flow path %q",
				u.Name, match[1], flowPath,
			)
		}
	}

	for _, param := range u.ForwardParams {
		if param == "*" {
			continue
		}

		if _, ok := flowParams[param]; !ok {
			return fmt.Errorf(
				"upstream %q: forward_param %q not declared in flow path %q",
				u.Name, param, flowPath,
			)
		}
	}

	return nil
}

func formatValidationError(err error) error {
	var ves validator.ValidationErrors
	if !errors.As(err, &ves) {
		return err
	}

	messages := make([]string, 0, len(ves))

	for _, fe := range ves {
		path := strings.TrimPrefix(fe.Namespace(), "Config.")
		messages = append(messages, fmt.Sprintf("  %s: %s", path, validationMessage(fe)))
	}

	return errors.New(strings.Join(messages, "\n"))
}

func validationMessage(fe validator.FieldError) string {
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
		return fmt.Sprintf("validation failed on %q", fe.Tag())
	}
}
