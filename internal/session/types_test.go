package session

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestSessionRecordRoundTripWithToolCallsAndCallID(t *testing.T) {
	text := "assistant reply"
	record := SessionRecord{
		ID:   "rec_1",
		Seq:  1,
		Kind: RecordMessage,
		Role: "assistant",
		Text: &text,
		ToolCalls: []ToolCall{
			{
				ID:                 "call_1",
				ToolID:             "tool_echo",
				DefinitionRevision: "rev-3",
				Name:               "echo",
				Arguments:          map[string]any{"message": "hi"},
			},
		},
		CallID:    "call_1",
		CreatedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		CharCount: 15,
		Tokens:    TokenCount{Value: 4, Source: TokenSourceTokenizer},
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded SessionRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.CallID != record.CallID {
		t.Fatalf("CallID = %q, want %q", decoded.CallID, record.CallID)
	}
	if len(decoded.ToolCalls) != 1 || !reflect.DeepEqual(decoded.ToolCalls, record.ToolCalls) {
		t.Fatalf("ToolCalls = %+v, want %+v", decoded.ToolCalls, record.ToolCalls)
	}
	if decoded.Text == nil || *decoded.Text != *record.Text {
		t.Fatalf("Text = %v, want %q", decoded.Text, *record.Text)
	}
}

func TestSessionRecordTextNilOmitsAndEmptyStringSerializes(t *testing.T) {
	empty := ""
	record := SessionRecord{Text: &empty}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !bytes.Contains(data, []byte(`"text":""`)) {
		t.Fatalf("empty Text not serialized as \"text\":\"\": %s", data)
	}

	data, err = json.Marshal(SessionRecord{})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if bytes.Contains(data, []byte(`"text"`)) {
		t.Fatalf("nil Text should be omitted from JSON: %s", data)
	}
}
