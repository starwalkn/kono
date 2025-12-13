package tokka

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type dispatcher interface {
	dispatch(route *Route, original *http.Request) [][]byte
}

type defaultDispatcher struct {
	client *http.Client
	log    *zap.Logger
}

func (d *defaultDispatcher) dispatch(route *Route, original *http.Request) [][]byte {
	results := make([][]byte, len(route.Backends))

	originalBody, readErr := io.ReadAll(original.Body)
	if readErr != nil {
		d.log.Error("cannot read body", zap.Error(readErr))
		return nil
	}
	if readErr = original.Body.Close(); readErr != nil {
		d.log.Warn("cannot close original request body", zap.Error(readErr))
	}

	var wg sync.WaitGroup

	for i, b := range route.Backends {
		wg.Add(1)

		go func(i int, b Backend, originalBody []byte) {
			defer wg.Done()

			method := b.Method
			if method == "" {
				// Fallback method.
				method = original.Method
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*time.Duration(b.Timeout))
			defer cancel()

			// Send request body only for body-acceptable methods requests.
			if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
				originalBody = nil
			}

			req, err := http.NewRequestWithContext(ctx, method, b.URL, bytes.NewReader(originalBody))
			if err != nil {
				results[i] = []byte(jsonErrInternal)
				return
			}

			d.resolveQueryStrings(b, req, original)
			d.resolveHeaders(b, req, original)

			d.log.Info("dispatching request", zap.String("method", method), zap.String("url", req.URL.String()))

			resp, err := d.client.Do(req)
			if err != nil {
				results[i] = []byte(jsonErrInternal)

				d.log.Error("backend request failed", zap.String("method", method), zap.Error(err))
				return
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				results[i] = []byte(jsonErrInternal)

				d.log.Error("cannot read backend response body", zap.Error(err))
				return
			}

			if err = resp.Body.Close(); err != nil {
				d.log.Warn("cannot close backend response body", zap.Error(err))
			}

			results[i] = body
		}(i, b, originalBody)
	}

	wg.Wait()

	return results
}

func (d *defaultDispatcher) resolveQueryStrings(b Backend, target, original *http.Request) {
	q := target.URL.Query()

	for _, fqs := range b.ForwardQueryStrings {
		if fqs == "*" {
			q = original.URL.Query()
			break
		}

		if original.URL.Query().Get(fqs) == "" {
			continue
		}

		q.Add(fqs, original.URL.Query().Get(fqs))
	}

	target.URL.RawQuery = q.Encode()
}

func (d *defaultDispatcher) resolveHeaders(b Backend, target, original *http.Request) {
	// Set forwarding headers.
	for _, fw := range b.ForwardHeaders {
		if fw == "*" {
			target.Header = original.Header.Clone()
			break
		}

		if strings.HasSuffix(fw, "*") {
			prefix := strings.TrimSuffix(fw, "*")

			for name, values := range original.Header {
				if strings.HasPrefix(name, prefix) {
					for _, v := range values {
						target.Header.Add(name, v)
					}
				}
			}

			continue
		}

		if original.Header.Get(fw) != "" {
			target.Header.Add(fw, original.Header.Get(fw))
		}
	}

	// Rewrite headers which exists in backend headers configuration (rewriting only forwarded headers).
	for header, value := range b.Headers {
		if !slices.Contains(b.ForwardHeaders, header) {
			continue
		}

		target.Header.Set(header, value)
	}

	// Always forward the Content-Type header.
	target.Header.Set("Content-Type", original.Header.Get("Content-Type"))
}
