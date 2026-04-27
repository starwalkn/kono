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

func okResponse(body string) upstreamResponse {
	return upstreamResponse{body: []byte(body)}
}

func errResponse(kind upstreamErrorKind) upstreamResponse {
	return upstreamResponse{
		err: &upstreamError{
			kind: kind,
			err:  fmt.Errorf("upstream error: %s", kind),
		},
	}
}

func jsonEqual(t *testing.T, expected string, actual []byte) {
	t.Helper()

	var e, a any
	require.NoError(t, json.Unmarshal([]byte(expected), &e), "invalid expected JSON")
	require.NoError(t, json.Unmarshal(actual, &a), "invalid actual JSON")
	assert.Equal(t, e, a)
}

type stubUpstream struct {
	upstreamName string
}

func (s *stubUpstream) name() string { return s.upstreamName }
func (s *stubUpstream) call(_ context.Context, _ *http.Request, _ []byte) *upstreamResponse {
	return &upstreamResponse{}
}

func stubUpstreams(names ...string) []upstream {
	upstreams := make([]upstream, len(names))
	for i, name := range names {
		upstreams[i] = &stubUpstream{upstreamName: name}
	}
	return upstreams
}

func TestAggregate_SingleResponse_Success(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{okResponse(`{"a":1}`)},
		aggregation{strategy: strategyArray},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	assert.False(t, result.partial)
	jsonEqual(t, `{"a":1}`, result.data)
}

func TestAggregate_SingleResponse_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{errResponse(upstreamTimeout)},
		aggregation{strategy: strategyArray},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.False(t, result.partial)
	require.Len(t, result.errors, 1)
	assert.Equal(t, ClientErrUpstreamUnavailable, result.errors[0])
}

func TestAggregate_SingleResponse_NilBody(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{{body: nil}},
		aggregation{strategy: strategyMerge},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.Empty(t, result.errors)
	assert.False(t, result.partial)
}

func TestAggregate_UnknownStrategy_ReturnsEmpty(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{okResponse(`{"a":1}`), okResponse(`{"b":2}`)},
		aggregation{strategy: aggregationStrategy(255)},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.Empty(t, result.errors)
}

func TestMerge_Success_OverwriteConflict(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":3,"c":4}`),
		},
		aggregation{
			strategy:       strategyMerge,
			conflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	jsonEqual(t, `{"a":1,"b":3,"c":4}`, result.data)
}

func TestMerge_ConflictPolicy_First(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		aggregation{
			strategy:       strategyMerge,
			conflictPolicy: conflictPolicyFirst,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	jsonEqual(t, `{"a":1,"b":2,"c":4}`, result.data)
}

func TestMerge_ConflictPolicy_Error(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		aggregation{
			strategy:       strategyMerge,
			conflictPolicy: conflictPolicyError,
		},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	require.Len(t, result.errors, 1)
	assert.Equal(t, ClientErrValueConflict, result.errors[0])
}

func TestMerge_ConflictPolicy_Prefer_SecondUpstream(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		aggregation{
			strategy:          strategyMerge,
			conflictPolicy:    conflictPolicyPrefer,
			preferredUpstream: 1,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	jsonEqual(t, `{"a":1,"b":99,"c":4}`, result.data)
}

func TestMerge_ConflictPolicy_Prefer_FirstUpstream(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1,"b":2}`),
			okResponse(`{"b":99,"c":4}`),
		},
		aggregation{
			strategy:          strategyMerge,
			conflictPolicy:    conflictPolicyPrefer,
			preferredUpstream: 0,
		},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	jsonEqual(t, `{"a":1,"b":2,"c":4}`, result.data)
}

func TestMerge_BestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1}`),
			errResponse(upstreamTimeout),
		},
		aggregation{
			strategy:       strategyMerge,
			bestEffort:     true,
			conflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.True(t, result.partial)
	require.Len(t, result.errors, 1)
	assert.Equal(t, ClientErrUpstreamUnavailable, result.errors[0])
	jsonEqual(t, `{"a":1}`, result.data)
}

func TestMerge_BestEffort_MalformedJSON(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1}`),
			{body: []byte(`not json`)},
		},
		aggregation{
			strategy:       strategyMerge,
			bestEffort:     true,
			conflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.True(t, result.partial)
	require.Len(t, result.errors, 1)
	assert.Equal(t, ClientErrUpstreamMalformed, result.errors[0])
	jsonEqual(t, `{"a":1}`, result.data)
}

func TestMerge_NoBestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1}`),
			errResponse(upstreamConnection),
		},
		aggregation{
			strategy:       strategyMerge,
			bestEffort:     false,
			conflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.False(t, result.partial)
	require.NotEmpty(t, result.errors)
}

func TestMerge_NoBestEffort_MalformedJSON(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"a":1}`),
			{body: []byte(`not json`)},
		},
		aggregation{
			strategy:       strategyMerge,
			bestEffort:     false,
			conflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.False(t, result.partial)
}

func TestMerge_ErrorDeduplication(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			errResponse(upstreamTimeout),
			errResponse(upstreamTimeout),
		},
		aggregation{
			strategy:       strategyMerge,
			bestEffort:     true,
			conflictPolicy: conflictPolicyOverwrite,
		},
		zap.NewNop(),
	)

	assert.Len(t, result.errors, 1)
}

func TestArray_Success(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"x":1}`),
			okResponse(`{"y":2}`),
		},
		aggregation{strategy: strategyArray},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	assert.False(t, result.partial)
	jsonEqual(t, `[{"x":1},{"y":2}]`, result.data)
}

func TestArray_BestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"x":1}`),
			errResponse(upstreamBadStatus),
		},
		aggregation{strategy: strategyArray, bestEffort: true},
		zap.NewNop(),
	)

	assert.True(t, result.partial)
	require.Len(t, result.errors, 1)
	assert.Equal(t, ClientErrUpstreamError, result.errors[0])
	jsonEqual(t, `[{"x":1}]`, result.data)
}

func TestArray_NoBestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		nil,
		[]upstreamResponse{
			okResponse(`{"x":1}`),
			errResponse(upstreamConnection),
		},
		aggregation{strategy: strategyArray, bestEffort: false},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.False(t, result.partial)
	require.NotEmpty(t, result.errors)
}

func TestNamespace_Success(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]upstreamResponse{
			okResponse(`{"id":1}`),
			okResponse(`{"total":99}`),
		},
		aggregation{strategy: strategyNamespace},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	jsonEqual(t, `{"users":{"id":1},"orders":{"total":99}}`, result.data)
}

func TestNamespace_BestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]upstreamResponse{
			okResponse(`{"id":1}`),
			errResponse(upstreamTimeout),
		},
		aggregation{strategy: strategyNamespace, bestEffort: true},
		zap.NewNop(),
	)

	assert.True(t, result.partial)
	require.Len(t, result.errors, 1)

	var got map[string]any
	require.NoError(t, json.Unmarshal(result.data, &got))
	assert.Contains(t, got, "users")
	assert.NotContains(t, got, "orders")
}

func TestNamespace_NoBestEffort_UpstreamError(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]upstreamResponse{
			okResponse(`{"id":1}`),
			errResponse(upstreamConnection),
		},
		aggregation{strategy: strategyNamespace, bestEffort: false},
		zap.NewNop(),
	)

	assert.Nil(t, result.data)
	assert.False(t, result.partial)
	require.NotEmpty(t, result.errors)
}

func TestNamespace_NilBody_WritesNull(t *testing.T) {
	agg := newTestAggregator()

	result := agg.aggregate(
		stubUpstreams("users", "orders"),
		[]upstreamResponse{
			okResponse(`{"id":1}`),
			{body: nil},
		},
		aggregation{strategy: strategyNamespace},
		zap.NewNop(),
	)

	require.Empty(t, result.errors)
	jsonEqual(t, `{"users":{"id":1},"orders":null}`, result.data)
}

func TestMapUpstreamError(t *testing.T) {
	agg := newTestAggregator()

	cases := []struct {
		kind upstreamErrorKind
		want ClientError
	}{
		{upstreamTimeout, ClientErrUpstreamUnavailable},
		{upstreamConnection, ClientErrUpstreamUnavailable},
		{upstreamBadStatus, ClientErrUpstreamError},
		{upstreamBodyTooLarge, ClientErrUpstreamBodyTooLarge},
		{upstreamInternal, ClientErrInternal},
		{upstreamReadError, ClientErrInternal},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := agg.mapUpstreamError(&upstreamError{
				kind: tc.kind,
				err:  errors.New("err"),
			})
			assert.Equal(t, tc.want, got)
		})
	}
}
