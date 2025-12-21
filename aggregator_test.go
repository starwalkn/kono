package tokka

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.uber.org/zap"
)

func newTestAggregator() *defaultAggregator {
	return &defaultAggregator{log: zap.NewNop()}
}

func makeUpstreamResponses(bodies [][]byte, errs []error) []UpstreamResponse {
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
	}, []error{nil, nil})

	gotBytes := agg.aggregate(responses, strategyMerge, false)

	var got map[string]any
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	want := map[string]any{
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
	}, []error{nil, nil})

	gotBytes := agg.aggregate(responses, strategyMerge, true)

	var got map[string]any
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	want := map[string]any{
		"a": float64(1),
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
	}, []error{nil, nil})

	gotBytes := agg.aggregate(responses, strategyMerge, false)
	if gotBytes != nil {
		t.Errorf("expected nil result, got %s", string(gotBytes))
	}
}

func TestAggregator_Array_Success(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"x":1}`),
		[]byte(`{"y":2}`),
	}, []error{nil, nil})

	gotBytes := agg.aggregate(responses, strategyArray, false)

	var got []map[string]any
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("failed to unmarshal array: %v", err)
	}

	want := []map[string]any{
		{"x": float64(1)},
		{"y": float64(2)},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAggregator_UnknownStrategy(t *testing.T) {
	agg := newTestAggregator()

	responses := makeUpstreamResponses([][]byte{
		[]byte(`{"a":1}`),
	}, []error{nil})

	gotBytes := agg.aggregate(responses, "unknown", false)
	if gotBytes != nil {
		t.Errorf("expected nil result for unknown strategy, got %s", string(gotBytes))
	}
}
