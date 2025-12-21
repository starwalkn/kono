package tokka

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

type GatewayConfig struct {
	Schema      string             `json:"schema" yaml:"schema" toml:"schema"`
	Name        string             `json:"name" yaml:"name" toml:"name"`
	Version     string             `json:"version" yaml:"version" toml:"version"`
	Debug       bool               `json:"debug" yaml:"debug" toml:"debug"`
	Server      ServerConfig       `json:"server" yaml:"server" toml:"server"`
	Dashboard   DashboardConfig    `json:"dashboard" yaml:"dashboard" toml:"dashboard"`
	Plugins     []CorePluginConfig `json:"plugins" yaml:"plugins" toml:"plugins"`
	Middlewares []MiddlewareConfig `json:"middlewares" yaml:"middlewares" toml:"middlewares"`
	Routes      []RouteConfig      `json:"routes" yaml:"routes" toml:"routes"`
}

type ServerConfig struct {
	Port          int  `json:"port" yaml:"port" toml:"port"`
	Timeout       int  `json:"timeout" yaml:"timeout" toml:"timeout"`
	EnableMetrics bool `json:"enable_metrics" yaml:"enable_metrics" toml:"enable_metrics"`
}

type DashboardConfig struct {
	Enable  bool `json:"enable" yaml:"enable" toml:"enable"`
	Port    int  `json:"port" yaml:"port" toml:"port"`
	Timeout int  `json:"timeout" yaml:"timeout" toml:"timeout"`
}

type RouteConfig struct {
	Path                string             `json:"path" yaml:"path" toml:"path"`
	Method              string             `json:"method" yaml:"method" toml:"method"`
	Plugins             []PluginConfig     `json:"plugins" yaml:"plugins" toml:"plugins"`
	Middlewares         []MiddlewareConfig `json:"middlewares" yaml:"middlewares" toml:"middlewares"`
	Upstreams           []UpstreamConfig   `json:"upstreams" yaml:"upstreams" toml:"upstreams"`
	Aggregate           string             `json:"aggregate" yaml:"aggregate" toml:"aggregate"`
	Transform           string             `json:"transform" yaml:"transform" toml:"transform"`
	AllowPartialResults bool               `json:"allow_partial_results" yaml:"allow_partial_results" toml:"allow_partial_results"`
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

	RetryConfig UpstreamRetryPolicyConfig `json:"retry" yaml:"retry" toml:"retry"`
}

type UpstreamRetryPolicyConfig struct {
	MaxRetries      int           `json:"max_retries" yaml:"max_retries" toml:"max_retries"`
	RetryOnStatuses []int         `json:"retry_on_statuses" yaml:"retry_on_statuses" toml:"retry_on_statuses"`
	BackoffDelay    time.Duration `json:"backoff_delay" yaml:"backoff_delay" toml:"backoff_delay"`
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

type CorePluginConfig struct {
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

//nolint:gocognit,gocyclo,cyclop // to be replace by sub-methods in future
func (cfg *GatewayConfig) Validate() error {
	var errs []error

	if cfg.Name == "" {
		errs = append(errs, errors.New("gateway.name is required"))
	}
	if cfg.Version == "" {
		errs = append(errs, errors.New("gateway.version is required"))
	}

	// Server config.
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		errs = append(errs, errors.New("server.port must be between 1 and 65535"))
	}
	if cfg.Server.Timeout <= 0 {
		errs = append(errs, errors.New("server.timeout must be > 0"))
	}

	// Dashboard config.
	if cfg.Dashboard.Enable {
		if cfg.Dashboard.Port <= 0 || cfg.Dashboard.Port > 65535 {
			errs = append(errs, errors.New("dashboard.port must be between 1 and 65535"))
		}
		if cfg.Dashboard.Timeout <= 0 {
			errs = append(errs, errors.New("dashboard.timeout must be > 0"))
		}
	}

	// Routes config.
	for i, route := range cfg.Routes {
		prefix := fmt.Sprintf("routes[%d]", i)

		if route.Path == "" {
			errs = append(errs, fmt.Errorf("%s.path is required", prefix))
		}
		if route.Method == "" {
			errs = append(errs, fmt.Errorf("%s.method is required", prefix))
		}
		if len(route.Upstreams) == 0 {
			errs = append(errs, fmt.Errorf("%s.upstreams must not be empty", prefix))
		}

		if route.Aggregate != "" && route.Aggregate != strategyArray && route.Aggregate != strategyMerge {
			errs = append(errs, fmt.Errorf("%s.aggregate must be 'array' or 'merge'", prefix))
		}

		// Upstreams config.
		for j, u := range route.Upstreams {
			upPrefix := fmt.Sprintf("%s.upstreams[%d]", prefix, j)

			if u.URL == "" {
				errs = append(errs, fmt.Errorf("%s.url is required", upPrefix))
			} else if _, err := url.Parse(u.URL); err != nil {
				errs = append(errs, fmt.Errorf("%s.url is invalid: %w", upPrefix, err))
			}

			if u.Timeout <= 0 {
				errs = append(errs, fmt.Errorf("%s.timeout must be > 0", upPrefix))
			}

			// Upstream policies config.
			p := u.Policy

			for _, code := range p.AllowedStatuses {
				if code < 100 || code > 599 {
					errs = append(errs, fmt.Errorf("%s.policy.allowed_status_codes contains invalid status %d", upPrefix, code))
				}
			}

			for from, to := range p.MapStatusCodes {
				if from < 100 || from > 599 || to < 100 || to > 599 {
					errs = append(errs, fmt.Errorf("%s.policy.map_status_codes has invalid mapping %d -> %d", upPrefix, from, to))
				}
			}

			if p.MaxResponseBodySize < 0 {
				errs = append(errs, fmt.Errorf("%s.policy.max_response_body_size must be >= 0", upPrefix))
			}

			// Upstream retry policies config.
			r := p.RetryConfig
			if r.MaxRetries < 0 {
				errs = append(errs, fmt.Errorf("%s.policy.retry.max_retries must be >= 0", upPrefix))
			}
			if r.BackoffDelay < 0 {
				errs = append(errs, fmt.Errorf("%s.policy.retry.backoff_delay must be >= 0", upPrefix))
			}
			for _, code := range r.RetryOnStatuses {
				if code < 100 || code > 599 {
					errs = append(errs, fmt.Errorf("%s.policy.retry.retry_on_statuses contains invalid status %d", upPrefix, code))
				}
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
