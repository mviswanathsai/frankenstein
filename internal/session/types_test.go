package session

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSessionRecordJSONRoundTripPreservesToolCallsAndCallID(t *testing.T) {
	text := ""
	want := SessionRecord{
		ID:     "record-1",
		Kind:   RecordToolCall,
		Text:   &text,
		CallID: "call-1",
		ToolCalls: []ToolCall{{
			ID:                 "call-1",
			ToolID:             "tool-1",
			DefinitionRevision: "rev-1",
			Name:               "lookup",
			Arguments:          map[string]any{"query": "cats"},
		}},
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got SessionRecord
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
