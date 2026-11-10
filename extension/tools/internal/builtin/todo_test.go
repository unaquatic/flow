package builtin

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTodo checks the list a todo call returns for its todos, as decoded
// from JSON, and that it fails on todos that are not a list of items with
// content and a known status.
func TestTodo(t *testing.T) {
	tests := map[string]struct {
		input   string
		want    string
		wantErr string
	}{
		"every status": {
			input: `{"todos":[{"content":"Find the tests","status":"completed"},{"content":"Add Divide","status":"in_progress"},{"content":"Run the tests","status":"pending"}]}`,
			want:  "[✓] Find the tests\n[–] Add Divide\n[ ] Run the tests",
		},
		"cancelled":      {input: `{"todos":[{"content":"Benchmark","status":"cancelled"}]}`, wantErr: `todos[0]: status "cancelled"`},
		"empty list":     {input: `{"todos":[]}`, want: NoTodos},
		"spaces joined":  {input: `{"todos":[{"content":"  Add\n  Divide ","status":"pending"}]}`, want: "[ ] Add Divide"},
		"encoded list":   {input: `{"todos":"[{\"content\":\"Add Divide\",\"status\":\"pending\"}]"}`, want: "[ ] Add Divide"},
		"missing todos":  {input: `{}`, wantErr: "missing todos"},
		"not a list":     {input: `{"todos":{"content":"Add Divide"}}`, wantErr: "must be an array"},
		"empty content":  {input: `{"todos":[{"content":"Add Divide","status":"pending"},{"content":" ","status":"pending"}]}`, wantErr: "todos[1]: content is empty"},
		"unknown status": {input: `{"todos":[{"content":"Add Divide","status":"done"}]}`, wantErr: `todos[0]: status "done"`},
		"no status":      {input: `{"todos":[{"content":"Add Divide"}]}`, wantErr: `todos[0]: status ""`},
	}
	todo := Todo()
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var args map[string]any
			if err := json.Unmarshal([]byte(tt.input), &args); err != nil {
				t.Fatal(err)
			}
			out, err := todo.Execute(t.Context(), args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v; want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Text != tt.want || out.IsError {
				t.Errorf("output = %q (error %v); want %q", out.Text, out.IsError, tt.want)
			}
		})
	}
}

// TestTodoListDone checks that a list counts its completed items.
func TestTodoListDone(t *testing.T) {
	list := TodoList{
		{Content: "a", Status: TodoCompleted},
		{Content: "b", Status: TodoInProgress},
		{Content: "c", Status: TodoPending},
		{Content: "d", Status: TodoCompleted},
	}
	if got := list.Done(); got != 2 {
		t.Errorf("Done() = %d; want 2", got)
	}
}

// TestTodoSchema checks that the schema describes each item of todos as an
// object with its content and one of TodoStatuses, as JSON Schema writes it.
func TestTodoSchema(t *testing.T) {
	b, err := json.Marshal(Todo().Schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"items":{"type":"object","properties":{`,
		`"enum":["pending","in_progress","completed"]`,
		`"required":["content","status"]`,
		`"required":["todos"]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("schema %s; want it to hold %s", b, want)
		}
	}
}
