package tokka

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

type httpUpstream struct {
	name                string
	url                 string
	method              string
	timeout             time.Duration
	headers             map[string]string
	forwardHeaders      []string
	forwardQueryStrings []string
	policy              UpstreamPolicy

	client *http.Client
}

func (u *httpUpstream) Name() string {
	return u.name
}

func (u *httpUpstream) Policy() UpstreamPolicy {
	return u.policy
}

func (u *httpUpstream) call(ctx context.Context, original *http.Request, originalBody []byte) *UpstreamResponse {
	uresp := &UpstreamResponse{
		Headers: make(http.Header, 0),
	}

	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	req, err := u.newRequest(ctx, original, originalBody)
	if err != nil {
		uresp.Err = err
		return uresp
	}

	hresp, err := u.client.Do(req)
	if err != nil {
		uresp.Err = err
		return uresp
	}
	defer hresp.Body.Close()

	uresp.Status = hresp.StatusCode
	uresp.Headers = hresp.Header.Clone()

	var reader io.Reader = hresp.Body
	if u.policy.MaxResponseBodySize > 0 {
		reader = io.LimitReader(hresp.Body, u.policy.MaxResponseBodySize+1)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		uresp.Err = err
		return uresp
	}

	if u.policy.MaxResponseBodySize > 0 && int64(len(body)) > u.policy.MaxResponseBodySize {
		uresp.Err = fmt.Errorf("response body larger than limit of %d bytes", u.policy.MaxResponseBodySize)
		return uresp
	}

	uresp.Body = body

	return uresp
}

func (u *httpUpstream) Call(ctx context.Context, original *http.Request, originalBody []byte, retryPolicy UpstreamRetryPolicy) *UpstreamResponse {
	resp := &UpstreamResponse{}

	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			resp.Err = ctx.Err()
			return resp
		default:
			resp = u.call(ctx, original, originalBody)
			if resp.Err == nil && !slices.Contains(retryPolicy.RetryOnStatuses, resp.Status) {
				break
			}

			if retryPolicy.BackoffDelay > 0 {
				select {
				case <-time.After(retryPolicy.BackoffDelay):
				case <-ctx.Done():
					resp.Err = ctx.Err()
					return resp
				}

				time.Sleep(retryPolicy.BackoffDelay)
			}
		}
	}

	return resp
}

func (u *httpUpstream) newRequest(ctx context.Context, original *http.Request, originalBody []byte) (*http.Request, error) {
	method := u.method
	if method == "" {
		// Fallback method.
		method = original.Method
	}

	// Send request body only for body-acceptable methods requests.
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		originalBody = nil
	}

	target, err := http.NewRequestWithContext(ctx, method, u.url, bytes.NewReader(originalBody))
	if err != nil {
		return nil, err
	}

	u.resolveQueryStrings(target, original)
	u.resolveHeaders(target, original)

	return target, nil
}

func (u *httpUpstream) resolveQueryStrings(target, original *http.Request) {
	q := target.URL.Query()

	for _, fqs := range u.forwardQueryStrings {
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

func (u *httpUpstream) resolveHeaders(target, original *http.Request) {
	// Set forwarding headers.
	for _, fw := range u.forwardHeaders {
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

	// Rewrite headers which exists in upstream headers configuration (rewriting only forwarded headers).
	for header, value := range u.headers {
		if slices.Contains(u.forwardHeaders, "*") || target.Header.Get(header) != "" {
			target.Header.Set(header, value)
		}
	}

	// Always forward the Content-Type header.
	target.Header.Set("Content-Type", original.Header.Get("Content-Type"))
}
