package tokka

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/tokka/internal/metric"
)

const maxBodySize = 5 << 20 // 5MB

type dispatcher interface {
	dispatch(route *Route, original *http.Request) []UpstreamResponse
}

type defaultDispatcher struct {
	log     *zap.Logger
	metrics metric.Metrics
}

// dispatch sends the incoming HTTP request to all upstreams configured for the given route.
// It reads and limits the request body, launches concurrent requests to upstreams using
// a semaphore to control parallelism, applies upstream policies (like allowed statuses,
// required body, status code mapping, max response size), updates metrics, and collects
// the responses into a slice. Any policy violations or request errors are wrapped in
// UpstreamError. The dispatcher waits for all upstream requests to complete before returning.
func (d *defaultDispatcher) dispatch(route *Route, original *http.Request) []UpstreamResponse {
	results := make([]UpstreamResponse, len(route.Upstreams))

	originalBody, readErr := io.ReadAll(io.LimitReader(original.Body, maxBodySize+1))
	if readErr != nil {
		d.log.Error("cannot read body", zap.Error(readErr))
		return nil
	}
	if readErr = original.Body.Close(); readErr != nil {
		d.log.Warn("cannot close original request body", zap.Error(readErr))
	}

	if len(originalBody) > maxBodySize {
		d.metrics.IncFailedRequestsTotal(metric.FailReasonBodyTooLarge)
		return nil
	}

	var (
		wg  = sync.WaitGroup{}
		sem = semaphore.NewWeighted(route.MaxParallelUpstreams)
	)

	for i, u := range route.Upstreams {
		wg.Add(1)

		go func(i int, u Upstream, originalBody []byte) {
			defer wg.Done()

			ctx := original.Context()

			if err := sem.Acquire(ctx, 1); err != nil {
				d.log.Error("cannot acquire semaphore", zap.Error(err))

				results[i] = UpstreamResponse{
					Err: &UpstreamError{
						Kind: UpstreamInternal,
						Err:  fmt.Errorf("semaphore acquire failed: %w", err),
					},
				}

				return
			}
			defer sem.Release(1)

			upstreamPolicy := u.Policy()

			resp := u.Call(ctx, original, originalBody, upstreamPolicy.RetryPolicy)
			if resp.Err != nil {
				d.metrics.IncFailedRequestsTotal(metric.FailReasonUpstreamError)
				d.log.Error("upstream request failed",
					zap.String("name", u.Name()),
					zap.Error(resp.Err.Unwrap()),
				)
			}

			if resp.Status != 0 {
				d.metrics.IncResponsesTotal(resp.Status)
			}

			var errs []error

			if upstreamPolicy.RequireBody && len(resp.Body) == 0 {
				errs = append(errs, errors.New("empty body not allowed by upstream policy"))
			}

			if mapped, ok := upstreamPolicy.MapStatusCodes[resp.Status]; ok {
				resp.Status = mapped
			}

			if len(upstreamPolicy.AllowedStatuses) > 0 && !slices.Contains(upstreamPolicy.AllowedStatuses, resp.Status) {
				errs = append(errs, fmt.Errorf("status %d not allowed by upstream policy", resp.Status))
			}

			if len(errs) > 0 {
				d.metrics.IncFailedRequestsTotal(metric.FailReasonPolicyViolation)

				if resp.Err == nil {
					resp.Err = &UpstreamError{
						Err: errors.Join(errs...),
					}
				} else {
					resp.Err.Err = errors.Join(resp.Err.Err, errors.Join(errs...))
				}
			}

			results[i] = *resp
		}(i, u, originalBody)
	}

	wg.Wait()

	return results
}
