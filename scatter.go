package kono

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/metric"
)

const maxBodySize = 5 << 20 // 5 MB

type scatter interface {
	scatter(f *flow, original *http.Request) []upstreamResponse
}

type defaultScatter struct {
	log     *zap.Logger
	metrics metric.Metrics
}

var wgPool = sync.Pool{
	New: func() interface{} { return &sync.WaitGroup{} },
}

// scatter reads the original request body once, then fans out to all upstreams
// concurrently, respecting the flow's parallelism semaphore.
// Returns nil when the body is unreadable or exceeds maxBodySize —
// the caller treats nil as a signal to respond with 413.
func (d *defaultScatter) scatter(f *flow, original *http.Request) []upstreamResponse {
	log := d.log.With(zap.String("request_id", requestIDFromContext(original.Context())))

	body, ok := d.readBody(original, log)
	if !ok {
		return nil
	}

	results := make([]upstreamResponse, len(f.upstreams))

	wg := wgPool.Get().(*sync.WaitGroup)
	wg.Add(len(f.upstreams))

	for i, u := range f.upstreams {
		go func() {
			defer wg.Done()
			results[i] = d.callUpstream(f, u, original, body, log)
		}()
	}

	wg.Wait()
	wgPool.Put(wg)

	return results
}

// readBody consumes and closes original.Body, enforcing maxBodySize.
// Returns (body, true) on success or (nil, false) on read error or oversized body.
func (d *defaultScatter) readBody(req *http.Request, log *zap.Logger) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodySize+1))
	if err != nil {
		log.Error("cannot read body", zap.Error(err))
		return nil, false
	}

	if err = req.Body.Close(); err != nil {
		log.Warn("cannot close original request body", zap.Error(err))
	}

	if len(body) > maxBodySize {
		d.metrics.IncFailedRequestsTotal(metric.FailReasonBodyTooLarge)
		return nil, false
	}

	return body, true
}

// callUpstream acquires the semaphore, calls the upstream, records metrics,
// and returns the response. Policy validation is handled by the upstream itself.
func (d *defaultScatter) callUpstream(
	f *flow,
	u upstream,
	original *http.Request,
	body []byte,
	log *zap.Logger,
) upstreamResponse {
	start := time.Now()
	ctx := original.Context()

	if err := f.sem.Acquire(ctx, 1); err != nil {
		log.Error("cannot acquire semaphore", zap.Error(err))

		return upstreamResponse{
			err: &upstreamError{
				kind: upstreamInternal,
				err:  fmt.Errorf("semaphore acquire failed: %w", err),
			},
		}
	}
	defer f.sem.Release(1)

	d.metrics.IncUpstreamRequestsTotal(f.path, u.name())

	resp := u.call(ctx, original, body)
	if resp.err != nil {
		d.metrics.IncUpstreamErrorsTotal(f.path, u.name(), string(resp.err.kind))
		log.Error("upstream call failed",
			zap.String("name", u.name()),
			zap.Error(resp.err.Unwrap()),
		)
	}

	d.metrics.UpdateUpstreamLatency(f.path, u.name(), start)

	return *resp
}
