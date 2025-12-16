package tokka

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/starwalkn/tokka/internal/metric"
	"go.uber.org/zap"
)

func TestDispatcher_Dispatch_Success(t *testing.T) {
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("A"))
	}))
	defer backendA.Close()

	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("B"))
	}))
	defer backendB.Close()

	d := &defaultDispatcher{
		client:  &http.Client{},
		log:     zap.NewNop(),
		metrics: metric.New(),
	}

	route := &Route{
		Backends: []Backend{
			{URL: backendA.URL, Timeout: 1000},
			{URL: backendB.URL, Timeout: 1000},
		},
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	got := string(bytes.Join(results, []byte{}))
	want := "AB"
	if got != want && got != "BA" { // Порядок не гарантирован
		t.Errorf("unexpected results: %q", got)
	}
}

func TestDispatcher_Dispatch_Timeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("too slow"))
	}))
	defer backend.Close()

	d := &defaultDispatcher{
		client:  &http.Client{},
		log:     zap.NewNop(),
		metrics: metric.New(),
	}

	route := &Route{
		Backends: []Backend{
			{URL: backend.URL, Timeout: 50},
		},
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)
	if string(results[0]) != jsonErrInternal {
		t.Errorf("expected internalError, got %q", results[0])
	}
}

func TestDispatcher_Dispatch_ForwardQueryAndHeaders(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("foo")
		h := r.Header.Get("X-Test")
		w.Write([]byte(q + "-" + h))
	}))
	defer backend.Close()

	d := &defaultDispatcher{
		client:  &http.Client{},
		log:     zap.NewNop(),
		metrics: metric.New(),
	}

	route := &Route{
		Backends: []Backend{
			{
				URL:                 backend.URL,
				ForwardQueryStrings: []string{"foo"},
				ForwardHeaders:      []string{"X-Test"},
				Timeout:             500,
			},
		},
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test?foo=bar", nil)
	originalRequest.Header.Set("X-Test", "baz")

	results := d.dispatch(route, originalRequest)

	if string(results[0]) != "bar-baz" {
		t.Errorf("unexpected result: %q", results[0])
	}
}

func TestDispatcher_Dispatch_PostWithBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	defer backend.Close()

	d := &defaultDispatcher{
		client:  &http.Client{},
		log:     zap.NewNop(),
		metrics: metric.New(),
	}

	route := &Route{
		Backends: []Backend{
			{URL: backend.URL, Timeout: 500},
		},
	}

	originalRequest := httptest.NewRequest(http.MethodPost, "http://example.com/test", bytes.NewBufferString("hello"))

	results := d.dispatch(route, originalRequest)

	if string(results[0]) != "hello" {
		t.Errorf("expected 'hello', got %q", results[0])
	}
}
