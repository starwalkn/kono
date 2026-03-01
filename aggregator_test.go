package kono

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.uber.org/zap"
)

func newTestAggregator() *defaultAggregator {
	return &defaultAggregator{log: zap.NewNop()}
}

func makeUpstreamResponses(bodies [][]byte, errs []*UpstreamError) []UpstreamResponse {
	responses := make([]UpstreamResponse, len(bodies))

	for i := range bodies {
		responses[i] = UpstreamResponse{
			Body: bodies[i],
			Err:  errs[i],
		}
	}

	return responses
}

func TestAggregator_Merge_Success(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"a":1,"b":2}`),
		[]byte(`{"b":3,"c":4}`),
	}, []*UpstreamError{nil, nil})

	aggregated := agg.aggregate(responses, Aggregation{
		Strategy:   strategyMerge,
		BestEffort: false,
	})

	var got map[string]interface{}
	if err := json.Unmarshal(aggregated.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	want := map[string]interface{}{
		"a": float64(1),
		"b": float64(3),
		"c": float64(4),
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAggregator_Merge_PartialAllowed(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"a":1}`),
		[]byte(`invalid json`),
	}, []*UpstreamError{nil, nil})

	aggregated := agg.aggregate(responses, Aggregation{
		Strategy:   strategyMerge,
		BestEffort: true,
	})

	var got map[string]interface{}
	if err := json.Unmarshal(aggregated.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	want := map[string]interface{}{
		"a": float64(1),
	}

	if len(aggregated.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(aggregated.Errors))
	}

	if !aggregated.Partial {
		t.Errorf("expected Partial=true")
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAggregator_Merge_PartialNotAllowed(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"a":1}`),
		[]byte(`invalid json`),
	}, []*UpstreamError{nil, nil})

	aggregated := agg.aggregate(responses, Aggregation{
		Strategy:   strategyMerge,
		BestEffort: false,
	})
	if aggregated.Data != nil {
		t.Errorf("expected nil result, got %s", string(aggregated.Data))
	}
}

func TestAggregator_Array_Success(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"x":1}`),
		[]byte(`{"y":2}`),
	}, []*UpstreamError{nil, nil})

	aggregated := agg.aggregate(responses, Aggregation{
		Strategy:   strategyArray,
		BestEffort: false,
	})

	var got []map[string]interface{}
	if err := json.Unmarshal(aggregated.Data, &got); err != nil {
		t.Fatalf("failed to unmarshal array: %v", err)
	}

	want := []map[string]interface{}{
		{"x": float64(1)},
		{"y": float64(2)},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAggregator_RawResponse(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"a":1}`),
	}, []*UpstreamError{nil})

	aggregated := agg.aggregate(responses, Aggregation{
		Strategy:   aggregationStrategy(255),
		BestEffort: false,
	})
	if aggregated.Data == nil {
		t.Errorf("expected nil result for unknown strategy, got %s", string(aggregated.Data))
	}

	if string(aggregated.Data) != (`{"a":1}`) {
		t.Errorf("got %s, want %s", string(aggregated.Data), string([]byte(`{"a":1}`)))
	}
}
