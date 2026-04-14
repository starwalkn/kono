package kono

import "time"

// upstreamPolicy defines the per-upstream configuration for handling HTTP responses, retries, and fault tolerance.
// Each upstream can have its own upstreamPolicy instance.
type upstreamPolicy struct {
	// Response validation
	headerBlacklist     map[string]struct{}
	allowedStatuses     []int
	requireBody         bool
	maxResponseBodySize int64

	// On-failure behaviour
	retry retryPolicy
}

// retryPolicy specifies retry behavior for an upstream, including max retries, which statuses trigger retries,
// and backoff delay between attempts.
type retryPolicy struct {
	maxRetries      int
	retryOnStatuses []int
	backoffDelay    time.Duration
}

type lbMode string

const (
	lbModeRoundRobin lbMode = "round_robin"
	lbModeLeastConns lbMode = "least_conns"
)
