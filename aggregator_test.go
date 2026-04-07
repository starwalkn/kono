package kono

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestAggregator() *defaultAggregator {
	return &defaultAggregator{}
}

func okResponse(body string) UpstreamResponse {
	return UpstreamResponse{Body: []byte(body)}
}

func errResponse(kind UpstreamErrorKind) UpstreamResponse {
	return UpstreamResponse{
		Err: &UpstreamError{
			Kind: kind,
			Err:  fmt.Errorf("upstream error: %s", kind),
		},
	}
}

// jsonEqual проверяет семантическое равенство двух JSON-значений.
func jsonEqual(t *testing.T, expected string, actual []byte) {
	t.Helper()

	var e, a any
	require.NoError(t, json.Unmarshal([]byte(expected), &e), "invalid expected JSON")
	require.NoError(t, json.Unmarshal(actual, &a), "invalid actual JSON")
	assert.Equal(t, e, a)
}

type stubUpstream struct {
	name string
}

func (s *stubUpstream) Name() string   { return s.name }
func (s *stubUpstream) Policy() Policy { return Policy{} }
func (s *stubUpstream) Call(_ context.Context, _ *http.Request, _ []byte) *UpstreamResponse {
	return &UpstreamResponse{}
}

func stubUpstreams(names ...string) []Upstream {
	upstreams := make([]Upstream, len(names))
	for i, name := range names {
		upstreams[i] = &stubUpstream{name: name}
	}
	return upstreams
}

func TestAggregate_SingleResponse_Success(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{okResponse(`{"a":1}`)},
		Aggregation{Strategy: strategyArray},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	assert.False(t, result.Partial)
	jsonEqual(t, `{"a":1}`, result.Data)
}

func TestAggregate_SingleResponse_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{errResponse(UpstreamTimeout)},
		Aggregation{Strategy: strategyArray},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.False(t, result.Partial)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, ClientErrUpstreamUnavailable, result.Errors[0])
}

func TestAggregate_SingleResponse_NilBody(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{{Body: nil}},
		Aggregation{Strategy: strategyMerge},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.Empty(t, result.Errors)
	assert.False(t, result.Partial)
}

func TestAggregate_UnknownStrategy_ReturnsEmpty(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{okResponse(`{"a":1}`), okResponse(`{"b":2}`)},
		Aggregation{Strategy: aggregationStrategy(255)},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.Empty(t, result.Errors)
}

func TestMerge_Success_OverwriteConflict(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":3,"c":4}`),
		},
		Aggregation{
			Strategy:       strategyMerge,
			ConflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	jsonEqual(t, `{"a":1,"b":3,"c":4}`, result.Data)
}

func TestMerge_ConflictPolicy_First(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		Aggregation{
			Strategy:       strategyMerge,
			ConflictPolicy: conflictPolicyFirst,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	jsonEqual(t, `{"a":1,"b":2,"c":4}`, result.Data)
}

func TestMerge_ConflictPolicy_Error(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		Aggregation{
			Strategy:       strategyMerge,
			ConflictPolicy: conflictPolicyError,
		},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, ClientErrValueConflict, result.Errors[0])
}

func TestMerge_ConflictPolicy_Prefer_SecondUpstream(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		Aggregation{
			Strategy:          strategyMerge,
			ConflictPolicy:    conflictPolicyPrefer,
			PreferredUpstream: 1,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	jsonEqual(t, `{"a":1,"b":99,"c":4}`, result.Data)
}

func TestMerge_ConflictPolicy_Prefer_FirstUpstream(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		Aggregation{
			Strategy:          strategyMerge,
			ConflictPolicy:    conflictPolicyPrefer,
			PreferredUpstream: 0,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	jsonEqual(t, `{"a":1,"b":2,"c":4}`, result.Data)
}

func TestMerge_BestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1}`),
			errResponse(UpstreamTimeout),
		},
		Aggregation{
			Strategy:       strategyMerge,
			BestEffort:     true,
			ConflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.True(t, result.Partial)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, ClientErrUpstreamUnavailable, result.Errors[0])
	jsonEqual(t, `{"a":1}`, result.Data)
}

func TestMerge_BestEffort_MalformedJSON(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1}`),
			{Body: []byte(`not json`)},
		},
		Aggregation{
			Strategy:       strategyMerge,
			BestEffort:     true,
			ConflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.True(t, result.Partial)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, ClientErrUpstreamMalformed, result.Errors[0])
	jsonEqual(t, `{"a":1}`, result.Data)
}

func TestMerge_NoBestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1}`),
			errResponse(UpstreamConnection),
		},
		Aggregation{
			Strategy:       strategyMerge,
			BestEffort:     false,
			ConflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.False(t, result.Partial)
	require.NotEmpty(t, result.Errors)
}

func TestMerge_NoBestEffort_MalformedJSON(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"a":1}`),
			{Body: []byte(`not json`)},
		},
		Aggregation{
			Strategy:       strategyMerge,
			BestEffort:     false,
			ConflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.False(t, result.Partial)
}

func TestMerge_ErrorDeduplication(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			errResponse(UpstreamTimeout),
			errResponse(UpstreamTimeout),
		},
		Aggregation{
			Strategy:       strategyMerge,
			BestEffort:     true,
			ConflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.Len(t, result.Errors, 1)
}

func TestArray_Success(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"x":1}`),
			okResponse(`{"y":2}`),
		},
		Aggregation{Strategy: strategyArray},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	assert.False(t, result.Partial)
	jsonEqual(t, `[{"x":1},{"y":2}]`, result.Data)
}

func TestArray_BestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"x":1}`),
			errResponse(UpstreamBadStatus),
		},
		Aggregation{Strategy: strategyArray, BestEffort: true},
		zap.NewNop(),
	)

	assert.True(t, result.Partial)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, ClientErrUpstreamError, result.Errors[0])
	jsonEqual(t, `[{"x":1}]`, result.Data)
}

func TestArray_NoBestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]UpstreamResponse{
			okResponse(`{"x":1}`),
			errResponse(UpstreamConnection),
		},
		Aggregation{Strategy: strategyArray, BestEffort: false},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.False(t, result.Partial)
	require.NotEmpty(t, result.Errors)
}

func TestNamespace_Success(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]UpstreamResponse{
			okResponse(`{"id":1}`),
			okResponse(`{"total":99}`),
		},
		Aggregation{Strategy: strategyNamespace},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	jsonEqual(t, `{"users":{"id":1},"orders":{"total":99}}`, result.Data)
}

func TestNamespace_BestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]UpstreamResponse{
			okResponse(`{"id":1}`),
			errResponse(UpstreamTimeout),
		},
		Aggregation{Strategy: strategyNamespace, BestEffort: true},
		zap.NewNop(),
	)

	assert.True(t, result.Partial)
	require.Len(t, result.Errors, 1)

	var got map[string]any
	require.NoError(t, json.Unmarshal(result.Data, &got))
	assert.Contains(t, got, "users")
	assert.NotContains(t, got, "orders")
}

func TestNamespace_NoBestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]UpstreamResponse{
			okResponse(`{"id":1}`),
			errResponse(UpstreamConnection),
		},
		Aggregation{Strategy: strategyNamespace, BestEffort: false},
		zap.NewNop(),
	)

	assert.Nil(t, result.Data)
	assert.False(t, result.Partial)
	require.NotEmpty(t, result.Errors)
}

func TestNamespace_NilBody_WritesNull(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]UpstreamResponse{
			okResponse(`{"id":1}`),
			{Body: nil},
		},
		Aggregation{Strategy: strategyNamespace},
		zap.NewNop(),
	)

	require.Empty(t, result.Errors)
	jsonEqual(t, `{"users":{"id":1},"orders":null}`, result.Data)
}

func TestMapUpstreamError(t *testing.T) {
	agg := newTestAggregator()

	cases := []struct {
		kind UpstreamErrorKind
		want ClientError
	}{
		{UpstreamTimeout, ClientErrUpstreamUnavailable},
		{UpstreamConnection, ClientErrUpstreamUnavailable},
		{UpstreamBadStatus, ClientErrUpstreamError},
		{UpstreamBodyTooLarge, ClientErrUpstreamBodyTooLarge},
		{UpstreamInternal, ClientErrInternal},
		{UpstreamReadError, ClientErrInternal},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := agg.mapUpstreamError(&UpstreamError{
				Kind: tc.kind,
				Err:  errors.New("err"),
			})
			assert.Equal(t, tc.want, got)
		})
	}
}
