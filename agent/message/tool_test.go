package message

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestToolPart_JSONRoundtripPending(t *testing.T) {
	tp := ToolPart{
		CallID: "call_1",
		Name:   "read",
		State:  ToolStatePending{Input: json.RawMessage(`{"path":"a.go"}`)},
	}
	parts := []ContentPart{tp}
	data, err := MarshalParts(parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 part, got %d", len(got))
	}
	out, ok := got[0].(ToolPart)
	if !ok {
		t.Fatalf("expected ToolPart, got %T", got[0])
	}
	if out.CallID != "call_1" || out.Name != "read" {
		t.Fatalf("identity lost: %+v", out)
	}
	pend, ok := out.State.(ToolStatePending)
	if !ok {
		t.Fatalf("expected pending state, got %T", out.State)
	}
	if string(pend.Input) != `{"path":"a.go"}` {
		t.Fatalf("input lost: %q", string(pend.Input))
	}
}

func TestToolPart_JSONRoundtripCompleted(t *testing.T) {
	tp := ToolPart{
		CallID: "call_2",
		Name:   "echo",
		State: ToolStateCompleted{
			Input:  json.RawMessage(`{"text":"hi"}`),
			Output: "hi",
			Title:  "echo hi",
		},
	}
	data, err := MarshalParts([]ContentPart{tp})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := got[0].(ToolPart)
	completed, ok := out.State.(ToolStateCompleted)
	if !ok {
		t.Fatalf("expected completed state, got %T", out.State)
	}
	if completed.Output != "hi" || completed.Title != "echo hi" {
		t.Fatalf("payload lost: %+v", completed)
	}
}

func TestToolState_JSONRoundtrip(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name  string
		state ToolState
	}{
		{name: "pending without input", state: ToolStatePending{}},
		{name: "pending with partial input", state: ToolStatePending{Input: json.RawMessage(`{"command":"go te`)}},
		{name: "error with partial input", state: ToolStateError{
			Input: json.RawMessage(`{"command":"go te`), Error: InterruptedError,
		}},
		{name: "running with output so far", state: ToolStateRunning{
			Input: json.RawMessage(`{"command":"ls"}`), Output: "a.go\n", StartedAt: started,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := MarshalParts([]ContentPart{ToolPart{ID: "prt_1", CallID: "call_1", Name: "bash", State: tt.state}})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := UnmarshalParts(data)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if state := got[0].(ToolPart).State; !reflect.DeepEqual(state, tt.state) {
				t.Errorf("state = %#v, want %#v", state, tt.state)
			}
		})
	}
}

func TestToolPart_IsTerminal(t *testing.T) {
	cases := []struct {
		state ToolState
		want  bool
	}{
		{ToolStatePending{}, false},
		{ToolStateRunning{}, false},
		{ToolStateCompleted{}, true},
		{ToolStateError{}, true},
	}
	for _, c := range cases {
		got := ToolPart{State: c.state}.IsTerminal()
		if got != c.want {
			t.Fatalf("state %T: got %v, want %v", c.state, got, c.want)
		}
	}
}
