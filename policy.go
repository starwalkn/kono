package kono

import "time"

// Policy defines the per-upstream configuration for handling HTTP responses, retries, and fault tolerance.
// Each upstream can have its own Policy instance.
type Policy struct {
	AllowedStatuses     []int
	RequireBody         bool
	MapStatusCodes      map[int]int
	MaxResponseBodySize int64

	RetryPolicy    RetryPolicy
	CircuitBreaker CircuitBreakerPolicy
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
