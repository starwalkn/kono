package tokka

import (
	"context"
	"net/http"
	"time"
)

type Upstream interface {
	Name() string
	Policy() UpstreamPolicy
	Call(ctx context.Context, original *http.Request, originalBody []byte) *UpstreamResponse
	callWithRetry(ctx context.Context, original *http.Request, originalBody []byte, retryPolicy UpstreamRetryPolicy) *UpstreamResponse
}

type UpstreamPolicy struct {
	AllowedStatuses     []int
	RequireBody         bool
	MapStatusCodes      map[int]int
	MaxResponseBodySize int64
	RetryPolicy         UpstreamRetryPolicy
}

type UpstreamRetryPolicy struct {
	MaxRetries      int
	RetryOnStatuses []int
	BackoffDelay    time.Duration
}

type UpstreamResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
	Err     error
}
