package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent"
)

// TodoStatus is where a todo item stands.
type TodoStatus string

// The statuses of a todo item.
const (
	// TodoPending is an item not started yet.
	TodoPending TodoStatus = "pending"
	// TodoInProgress is the item being worked on.
	TodoInProgress TodoStatus = "in_progress"
	// TodoCompleted is a finished item.
	TodoCompleted TodoStatus = "completed"
)

// TodoStatuses are the statuses of a todo item, in the order work moves
// through them.
var TodoStatuses = []TodoStatus{TodoPending, TodoInProgress, TodoCompleted}

// Mark returns the box an item of status s is listed with: "[ ]" pending,
// "[–]" in progress, and "[✓]" completed.
func (s TodoStatus) Mark() string {
	switch s {
	case TodoInProgress:
		return "[–]"
	case TodoCompleted:
		return "[✓]"
	}
	return "[ ]"
}

// TodoItem is one step of a todo list.
type TodoItem struct {
	// Content says what the step does.
	Content string `json:"content"`
	// Status is where the step stands.
	Status TodoStatus `json:"status"`
}

// TodoList is the todo list a call of the todo tool writes, in order.
type TodoList []TodoItem

// NoTodos is what an empty TodoList lists as.
const NoTodos = "[no todos]"

// Todo returns a tool that writes the model's todo list, replacing it whole
// on each call, and returns the list as TodoList.String lists it. The list
// lives in the calls themselves, in the session's history, so the tool keeps
// no state.
func Todo() agent.Tool {
	statuses := make([]string, len(TodoStatuses))
	for i, s := range TodoStatuses {
		statuses[i] = string(s)
	}
	return agent.Tool{
		Name: "todo",
		Description: "Write your todo list for the current task, replacing the whole list. Use it to plan and track work of three or more distinct steps, or when the user gives several tasks: " +
			"add the steps as pending, mark one in_progress before you start it, and mark it completed as soon as it is done, before you start the next. " +
			"Keep exactly one step in_progress while work remains. Remove steps that are no longer needed, and add steps you discover as you go. " +
			"Skip it for a single, simple step. Returns the list.",
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"todos": {
					Type:        "array",
					Description: "The whole todo list, in order.",
					Items: &agent.PropertySchema{
						Type: "object",
						Properties: map[string]agent.PropertySchema{
							"content": {Type: "string", Description: "What the step does, in a few words, such as \"Run the calculator tests\"."},
							"status":  {Type: "string", Description: "Where the step stands.", Enum: statuses},
						},
						Required: []string{"content", "status"},
					},
				},
			},
			Required: []string{"todos"},
		},
		Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
			v, ok := args["todos"]
			if !ok {
				return agent.ToolOutput{}, errors.New("missing todos")
			}
			list, err := ParseTodos(v)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: list.String()}, nil
		},
	}
}

// ParseTodos returns the todo list that v, the todos argument of a todo
// call, holds: an array of items, as JSON decodes into a map[string]any, or
// that array encoded as a JSON string, as some models send it. Each item
// needs content and one of TodoStatuses. Each run of spaces in its content,
// newlines among them, is joined into one space, so that the item lists on
// one line, and the spaces around it are dropped.
func ParseTodos(v any) (TodoList, error) {
	data, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("todos: %w", err)
		}
		data = string(b)
	}
	var list TodoList
	if err := json.Unmarshal([]byte(data), &list); err != nil {
		return nil, fmt.Errorf("todos must be an array of items with content and status: %w", err)
	}
	for i, item := range list {
		list[i].Content = strings.Join(strings.Fields(item.Content), " ")
		if list[i].Content == "" {
			return nil, fmt.Errorf("todos[%d]: content is empty", i)
		}
		if !slices.Contains(TodoStatuses, item.Status) {
			return nil, fmt.Errorf("todos[%d]: unknown status: %s; use one of %v", i, item.Status, TodoStatuses)
		}
	}
	return list, nil
}

// Done returns the number of completed items.
func (l TodoList) Done() int {
	done := 0
	for _, item := range l {
		if item.Status == TodoCompleted {
			done++
		}
	}
	return done
}

// String returns the item as it lists: its content after its status's Mark,
// as "[✓] Find the tests".
func (i TodoItem) String() string { return i.Status.Mark() + " " + i.Content }

// String lists the items one per line, as TodoItem.String gives them, or
// returns NoTodos when there are none.
func (l TodoList) String() string {
	if len(l) == 0 {
		return NoTodos
	}
	var b strings.Builder
	for i, item := range l {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(item.String())
	}
	return b.String()
}
