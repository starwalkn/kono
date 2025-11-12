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

func TestAggregator_Merge_Success(t *testing.T) {
	agg := newTestAggregator()

	responses := [][]byte{
		[]byte(`{"a":1,"b":2}`),
		[]byte(`{"b":3,"c":4}`),
	}

	gotBytes := agg.aggregate(responses, strategyMerge, false)

	var got map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
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

	responses := [][]byte{
		[]byte(`{"a":1}`),
		[]byte(`invalid json`),
	}

	gotBytes := agg.aggregate(responses, strategyMerge, true)

	var got map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	want := map[string]interface{}{
		"a": float64(1),
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAggregator_Merge_PartialNotAllowed(t *testing.T) {
	agg := newTestAggregator()

	responses := [][]byte{
		[]byte(`{"a":1}`),
		[]byte(`invalid json`),
	}

	gotBytes := agg.aggregate(responses, strategyMerge, false)
	if gotBytes != nil {
		t.Errorf("expected nil result, got %s", string(gotBytes))
	}
}

func TestAggregator_Array_Success(t *testing.T) {
	agg := newTestAggregator()

	responses := [][]byte{
		[]byte(`{"x":1}`),
		[]byte(`{"y":2}`),
	}

	gotBytes := agg.aggregate(responses, strategyArray, false)

	var got []map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
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

func TestAggregator_UnknownStrategy(t *testing.T) {
	agg := newTestAggregator()

	responses := [][]byte{
		[]byte(`{"a":1}`),
	}

	gotBytes := agg.aggregate(responses, "unknown", false)
	if gotBytes != nil {
		t.Errorf("expected nil result for unknown strategy, got %s", string(gotBytes))
	}
}
