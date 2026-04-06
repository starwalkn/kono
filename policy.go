package kono

import "time"

// Policy defines the per-upstream configuration for handling HTTP responses, retries, and fault tolerance.
// Each upstream can have its own Policy instance.
type Policy struct {
	// Response validation
	HeaderBlacklist     map[string]struct{}
	AllowedStatuses     []int
	RequireBody         bool
	MaxResponseBodySize int64

	// On-failure behaviour
	Retry RetryPolicy
}

// RetryPolicy specifies retry behavior for an upstream, including max retries, which statuses trigger retries,
// and backoff delay between attempts.
type RetryPolicy struct {
	MaxRetries      int
	RetryOnStatuses []int
	BackoffDelay    time.Duration
}

// CircuitBreakerPolicy configures a per-upstream circuit breaker, including maximum consecutive failures,
// and the reset timeout after which the breaker will allow attempts again.
type CircuitBreakerPolicy struct {
	Enabled      bool
	MaxFailures  int
	ResetTimeout time.Duration
}

type LBMode string

const (
	lbModeRoundRobin LBMode = "round_robin"
	lbModeLeastConns LBMode = "least_conns"
)
