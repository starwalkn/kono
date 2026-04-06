package kono

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/metric"
)

const maxBodySize = 5 << 20 // 5MB

type dispatcher interface {
	dispatch(route *Flow, original *http.Request) []UpstreamResponse
}

type defaultDispatcher struct {
	log     *zap.Logger
	metrics metric.Metrics
}

var wgPool = sync.Pool{
	New: func() interface{} {
		return &sync.WaitGroup{}
	},
}

// dispatch sends the incoming HTTP request to all upstreams configured for the given route.
// It reads and limits the request body, launches concurrent requests to upstreams using
// a semaphore to control parallelism, applies upstream policies (like allowed statuses,
// required body, status code mapping, max response size), updates metrics, and collects
// the responses into a slice. Any policy violations or request errors are wrapped in
// UpstreamError. The dispatcher waits for all upstream requests to complete before returning.
func (d *defaultDispatcher) dispatch(flow *Flow, original *http.Request) []UpstreamResponse {
	log := d.log.With(zap.String("request_id", requestIDFromContext(original.Context())))

	results := make([]UpstreamResponse, len(flow.Upstreams))

	originalBody, readErr := io.ReadAll(io.LimitReader(original.Body, maxBodySize+1))
	if readErr != nil {
		log.Error("cannot read body", zap.Error(readErr))
		return nil
	}
	if readErr = original.Body.Close(); readErr != nil {
		log.Warn("cannot close original request body", zap.Error(readErr))
	}

	if len(originalBody) > maxBodySize {
		d.metrics.IncFailedRequestsTotal(metric.FailReasonBodyTooLarge)
		return nil
	}

	wg := wgPool.Get().(*sync.WaitGroup)
	wg.Add(len(flow.Upstreams))

	for i, u := range flow.Upstreams {
		go func(i int, u Upstream, originalBody []byte) {
			defer wg.Done()

			start := time.Now()

			ctx := original.Context()

			if err := flow.sem.Acquire(ctx, 1); err != nil {
				log.Error("cannot acquire semaphore", zap.Error(err))

				results[i] = UpstreamResponse{
					Err: &UpstreamError{
						Kind: UpstreamInternal,
						Err:  fmt.Errorf("semaphore acquire failed: %w", err),
					},
				}

				return
			}
			defer flow.sem.Release(1)

			d.metrics.IncUpstreamRequestsTotal(flow.Path, u.Name())

			resp := u.Call(ctx, original, originalBody)
			if resp.Err != nil {
				d.metrics.IncUpstreamErrorsTotal(flow.Path, u.Name(), string(resp.Err.Kind))
				log.Error("upstream request failed",
					zap.String("name", u.Name()),
					zap.Error(resp.Err.Unwrap()),
				)
			}

			// Handle upstream policies
			var (
				errs           []error
				upstreamPolicy = u.Policy()
			)

			if upstreamPolicy.RequireBody && len(resp.Body) == 0 {
				errs = append(errs, errors.New("empty body not allowed by upstream policy"))
			}

			if len(upstreamPolicy.AllowedStatuses) > 0 && !slices.Contains(upstreamPolicy.AllowedStatuses, resp.Status) {
				errs = append(errs, fmt.Errorf("status %d not allowed by upstream policy", resp.Status))
			}

			if len(errs) > 0 {
				d.metrics.IncUpstreamErrorsTotal(flow.Path, u.Name(), "policy_violation")

				if resp.Err == nil {
					resp.Err = &UpstreamError{
						Err: errors.Join(errs...),
					}
				} else {
					resp.Err.Err = errors.Join(resp.Err.Err, errors.Join(errs...))
				}
			}

			d.metrics.UpdateUpstreamLatency(flow.Path, u.Name(), start)

			results[i] = *resp
		}(i, u, originalBody)
	}

	wg.Wait()
	wgPool.Put(wg)

	return results
}
