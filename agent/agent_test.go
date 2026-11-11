package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/agenttest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
)

// newTools returns Tools holding tools.
func newTools(tools ...agent.Tool) *agent.Tools {
	s := agent.NewTools()
	for _, t := range tools {
		s.Register(t)
	}
	return s
}

// runAgent runs a and returns the messages of its completed turns, as
// MessageEnded carries them, the events it emitted, and its error.
func runAgent(t *testing.T, ctx context.Context, a *agent.Agent) ([]message.Content, []agent.Event, error) {
	t.Helper()
	var events []agent.Event
	a.OnEvent = func(ev agent.Event) { events = append(events, ev) }

	err := a.Run(ctx)
	var turns []message.Content
	for _, e := range eventsOfType[agent.MessageEnded](events) {
		if e.Err == nil && !e.Stopped {
			turns = append(turns, e.Message)
		}
	}
	return turns, events, err
}

// toolCalls returns the number of tool calls in m.
func toolCalls(m message.Content) int {
	n := 0
	for _, p := range m.Parts {
		if _, ok := p.(message.ToolPart); ok {
			n++
		}
	}
	return n
}

// eventsOfType returns events with concrete type E.
func eventsOfType[E agent.Event](events []agent.Event) []E {
	var out []E
	for _, ev := range events {
		if typed, ok := ev.(E); ok {
			out = append(out, typed)
		}
	}
	return out
}

func TestAgent_StreamError(t *testing.T) {
	wantErr := errors.New("connection refused")

	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.StreamError(wantErr), nil }}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
	}

	_, events, err := runAgent(t, context.Background(), ag)

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	ended := eventsOfType[agent.MessageEnded](events)
	if len(ended) != 1 || ended[0].Err == nil || ended[0].Stopped {
		t.Fatalf("MessageEnded = %+v, want one with the error", ended)
	}
}

func TestAgent_GatewayError(t *testing.T) {
	wantErr := errors.New("POST failed: EOF")

	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return nil, wantErr }}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)

	if len(turns) != 0 {
		t.Fatalf("no turn should complete on gateway error, got %d", len(turns))
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestAgent_SimpleTextResponse(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.TextResponse("Hello!"), nil }}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, events, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if toolCalls(turns[0]) != 0 {
		t.Fatalf("expected no tools executed, got %d", toolCalls(turns[0]))
	}
	if len(turns[0].Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(turns[0].Parts))
	}
	tp, ok := turns[0].Parts[0].(message.TextPart)
	if !ok || tp.Text != "Hello!" {
		t.Fatalf("expected TextPart 'Hello!', got %+v", turns[0].Parts[0])
	}

	started := eventsOfType[agent.MessageStarted](events)
	if len(started) != 1 || started[0].Message.ID != turns[0].ID {
		t.Fatalf("MessageStarted = %+v, want one for %q", started, turns[0].ID)
	}
	ended := eventsOfType[agent.MessageEnded](events)
	if len(ended) != 1 || ended[0].Message.ID != turns[0].ID {
		t.Fatalf("MessageEnded = %+v, want one for %q", ended, turns[0].ID)
	}
	if e := ended[0]; e.Finish != agent.StopEndTurn || e.Stopped || e.Err != nil {
		t.Errorf("MessageEnded = %+v, want finished at end_turn", e)
	}
}

// TestAgent_CacheKey checks that every request of a run names the agent's
// session as its cache key, so the provider can serve the conversation from
// one prompt cache.
func TestAgent_CacheKey(t *testing.T) {
	responses := []agent.StreamResponse{
		agenttest.ToolCallResponse("call_1", "echo", `{}`),
		agenttest.TextResponse("done"),
	}
	gw := &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		r := responses[0]
		responses = responses[1:]
		return r, nil
	}}
	echo := agent.Tool{
		Name: "echo",
		Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
			return agent.ToolOutput{Text: "echoed"}, nil
		},
	}
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, gw, "test"),
		Tools:     newTools(echo),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		SessionID: "sess-1",
	}

	if _, _, err := runAgent(t, context.Background(), ag); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"sess-1", "sess-1"}; !slices.Equal(gw.StreamCacheKeys, want) {
		t.Errorf("cache keys = %q, want %q", gw.StreamCacheKeys, want)
	}
}

func TestAgent_ToolCallLoop(t *testing.T) {
	call, executed := 0, 0
	tools := newTools(agent.Tool{
		Name: "echo",
		Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
			executed++
			return agent.ToolOutput{Text: "echoed"}, nil
		},
	})

	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			call++
			if call == 1 {
				return agenttest.ToolCallResponse("call_1", "echo", `{"text":"hello"}`), nil
			}
			return agenttest.TextResponse("Done!"), nil
		}}, "test"),
		Tools:    tools,
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, events, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if toolCalls(turns[0]) != 1 {
		t.Fatalf("turn 1 should report 1 tool call, got %d", toolCalls(turns[0]))
	}
	if toolCalls(turns[1]) != 0 {
		t.Fatalf("turn 2 should report 0 tool calls, got %d", toolCalls(turns[1]))
	}
	if executed != 1 {
		t.Fatalf("echo tool ran %d times, want 1", executed)
	}

	// Part events show the call's states in order under one part ID.
	var states []string
	var ids []message.PartID
	_, changes := partChangesOf(events)
	for _, c := range changes {
		tp, ok := c.Part.(message.ToolPart)
		if !ok {
			continue
		}
		state := fmt.Sprintf("%T", tp.State)
		if len(states) == 0 || states[len(states)-1] != state {
			states = append(states, state)
		}
		if !slices.Contains(ids, tp.ID) {
			ids = append(ids, tp.ID)
		}
	}
	wantStates := []string{"message.ToolStatePending", "message.ToolStateRunning", "message.ToolStateCompleted"}
	if !slices.Equal(states, wantStates) {
		t.Fatalf("tool states in part events = %q, want %q", states, wantStates)
	}
	if len(ids) != 1 {
		t.Fatalf("tool part IDs in part events = %q, want one", ids)
	}

	if len(ag.Messages) != 3 {
		t.Fatalf("expected 3 messages in history, got %d", len(ag.Messages))
	}
	var found bool
	for _, p := range ag.Messages[1].Content {
		tp, ok := p.(message.ToolPart)
		if !ok {
			continue
		}
		if _, completed := tp.State.(message.ToolStateCompleted); !completed {
			t.Fatalf("expected ToolStateCompleted, got %T", tp.State)
		}
		found = true
	}
	if !found {
		t.Fatal("expected a ToolPart on the first agent message")
	}
}

// TestAgent_ToolsExecuteUnconditionally checks that requested tools run
// without approval.
func TestAgent_ToolsExecuteUnconditionally(t *testing.T) {
	call, executed := 0, 0
	tools := newTools(agent.Tool{
		Name: "bash",
		Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
			executed++
			return agent.ToolOutput{Text: "file1.go\nfile2.go"}, nil
		},
	})

	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			call++
			if call == 1 {
				return agenttest.ToolCallResponse("call_1", "bash", `{"command":"ls"}`), nil
			}
			return agenttest.TextResponse("Here are the files."), nil
		}}, "test"),
		Tools:    tools,
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("list files")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed != 1 {
		t.Fatalf("bash tool ran %d times, want 1", executed)
	}
	if len(turns) != 2 || toolCalls(turns[0]) != 1 {
		t.Fatalf("expected 2 turns with 1 tool call first, got %d turns", len(turns))
	}

	var tp *message.ToolPart
	for _, p := range turns[0].Parts {
		if v, ok := p.(message.ToolPart); ok {
			tp = &v
		}
	}
	if tp == nil {
		t.Fatal("expected a ToolPart in the turn result")
	}
	done, ok := tp.State.(message.ToolStateCompleted)
	if !ok {
		t.Fatalf("expected ToolStateCompleted, got %T", tp.State)
	}
	if done.Output != "file1.go\nfile2.go" {
		t.Fatalf("unexpected tool output: %q", done.Output)
	}
}

// TestAgent_ParallelCallsGetTheirOwnArguments checks that parallel calls run
// with their own arguments, whether these stream interleaved or arrive only
// as the call ends, and that calls the provider never ended fail without
// running, with the input they had.
func TestAgent_ParallelCallsGetTheirOwnArguments(t *testing.T) {
	const pwd, ls, cat = `{"command":"pwd"}`, `{"command":"ls"}`, `{"command":"cat go.mod"}`
	var ran []string
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ToolUseStart{ID: "call_a", Name: "bash"},
				agent.ToolUseStart{ID: "call_b", Name: "bash"},
				agent.ToolUseStart{ID: "call_c", Name: "bash"},
				agent.ToolUseStart{ID: "call_d", Name: "bash"},
				agent.ToolUseStart{ID: "call_e", Name: "bash"},
				agent.ToolUseDelta{ID: "call_b", JSON: `{"command":`},
				agent.ToolUseDelta{ID: "call_a", JSON: pwd},
				agent.ToolUseDelta{ID: "call_e", JSON: `{"command":"rm`},
				agent.ToolUseDelta{ID: "call_b", JSON: `"ls"}`},
				agent.ToolUseEnd{ID: "call_a", Input: json.RawMessage(pwd)},
				agent.ToolUseEnd{ID: "call_b", Input: json.RawMessage(ls)},
				agent.ToolUseEnd{ID: "call_c", Input: json.RawMessage(cat)},
				agent.Stop{Reason: agent.StopToolUse},
			), nil
		}}, "test"),
		Tools: newTools(agent.Tool{
			Name: "bash",
			Execute: func(_ context.Context, args map[string]any) (agent.ToolOutput, error) {
				command, _ := args["command"].(string)
				ran = append(ran, command)
				return agent.ToolOutput{Text: "ok"}, nil
			},
		}),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("explore")},
		MaxSteps: 1,
	}

	turns, _, _ := runAgent(t, context.Background(), ag)
	if want := []string{"pwd", "ls", "cat go.mod"}; !slices.Equal(ran, want) {
		t.Errorf("commands run = %q, want %q", ran, want)
	}
	if len(turns) != 1 || toolCalls(turns[0]) != 5 {
		t.Fatalf("turns = %+v, want one with the 5 calls", turns)
	}
	wantInputs := map[string]string{"call_a": pwd, "call_b": ls, "call_c": cat, "call_d": "", "call_e": `{"command":"rm`}
	for _, p := range turns[0].Parts {
		tp := p.(message.ToolPart)
		if got := string(tp.Input()); got != wantInputs[tp.CallID] {
			t.Errorf("%s input = %q, want %q", tp.CallID, got, wantInputs[tp.CallID])
		}
		st, failed := tp.State.(message.ToolStateError)
		switch tp.CallID {
		case "call_d", "call_e":
			if !failed || !strings.HasPrefix(st.Error, agent.ErrIncompleteToolInput.Error()) {
				t.Errorf("%s state = %+v, want an error that its arguments did not arrive in full", tp.CallID, tp.State)
			}
		default:
			if _, ok := tp.State.(message.ToolStateCompleted); !ok {
				t.Errorf("%s state = %+v, want completed", tp.CallID, tp.State)
			}
		}
	}
}

func TestAgent_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return func(yield func(agent.StreamPart, error) bool) {
				yield(agent.TextDelta{Text: "hel"}, nil)
				cancel()
				yield(agent.TextDelta{Text: "lo"}, nil)
			}, nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	_, events, err := runAgent(t, ctx, ag)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Cancellation retains text received before the context ended, and
	// ends its part.
	types, changes := partChangesOf(events)
	if want := []partKind{partStarted, partEnded}; !slices.Equal(types, want) {
		t.Fatalf("part events = %v, want %v", types, want)
	}
	for _, c := range changes {
		if tp, ok := c.Part.(message.TextPart); !ok || tp.Text != "hel" {
			t.Fatalf("expected partial text %q to survive cancellation, got %+v", "hel", c.Part)
		}
	}
}

// recordStore is a MessageStore that keeps a copy of each message it is
// given, and the session it is stored in, or fails with err, if set, and
// keeps nothing. As a database does, it fails once its context has ended.
type recordStore struct {
	msgs     []message.Content
	sessions []message.SessionID
	err      error
}

func (s *recordStore) AppendMessage(ctx context.Context, sessionID message.SessionID, msg *message.Content) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	m := *msg
	m.Parts = slices.Clone(msg.Parts)
	s.msgs = append(s.msgs, m)
	s.sessions = append(s.sessions, sessionID)
	return nil
}

// TestAgent_ReportsMessageNotStored checks that a message that cannot be
// stored ends with an error, also when the turn was stopped, and that the
// turn's error wraps ErrNotStored.
func TestAgent_ReportsMessageNotStored(t *testing.T) {
	tests := []struct {
		name string
		stop bool
	}{
		{name: "finished"},
		{name: "stopped", stop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ag := &agent.Agent{
				Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
					return func(yield func(agent.StreamPart, error) bool) {
						yield(agent.TextDelta{Text: "hel"}, nil)
						if tt.stop {
							cancel()
						}
						yield(agent.TextDelta{Text: "lo"}, nil)
					}, nil
				}}, "test"),
				Tools:    newTools(),
				Variant:  agent.Variant{Provider: "test", Model: "test-model"},
				Messages: []agent.Message{agent.NewUserMessage("hi")},
				Store:    &recordStore{err: errors.New("disk full")},
			}

			_, events, err := runAgent(t, ctx, ag)
			if !errors.Is(err, agent.ErrNotStored) {
				t.Errorf("run error = %v, want ErrNotStored", err)
			}
			if tt.stop && errors.Is(err, context.Canceled) {
				t.Errorf("run error = %v, want only the failure to store", err)
			}
			ended := eventsOfType[agent.MessageEnded](events)
			if len(ended) != 1 {
				t.Fatalf("MessageEnded = %+v, want one", ended)
			}
			e := ended[0]
			if e.Stopped != tt.stop || e.Err == nil || !strings.Contains(e.Err.Error(), "disk full") {
				t.Errorf("MessageEnded stopped, error = %v, %+v, want %v and the failure to store", e.Stopped, e.Err, tt.stop)
			}
		})
	}
}

// TestAgent_StoresMessageWhenTurnEnds checks that a turn's message is stored
// once, in the run's session, after its tools have run, with its tool calls
// and token usage.
func TestAgent_StoresMessageWhenTurnEnds(t *testing.T) {
	var storedMidStream, storedDuringTool int
	store := &recordStore{}
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return func(yield func(agent.StreamPart, error) bool) {
				yield(agent.TextDelta{Text: "Listing."}, nil)
				yield(agent.ToolUseStart{ID: "call_1", Name: "echo"}, nil)
				storedMidStream = len(store.msgs)
				yield(agent.ToolUseDelta{ID: "call_1", JSON: `{"text":"hi"}`}, nil)
				yield(agent.ToolUseEnd{ID: "call_1", Input: json.RawMessage(`{"text":"hi"}`)}, nil)
				yield(agent.Usage{InputTokens: 7, OutputTokens: 3}, nil)
				yield(agent.Stop{Reason: agent.StopToolUse}, nil)
			}, nil
		}}, "test"),
		Tools: newTools(agent.Tool{
			Name: "echo",
			Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
				storedDuringTool = len(store.msgs)
				return agent.ToolOutput{Text: "hi"}, nil
			},
		}),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		SessionID: "ses_run",
		Store:     store,
		MaxSteps:  1,
	}

	turns, _, _ := runAgent(t, context.Background(), ag)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if storedMidStream != 0 || storedDuringTool != 0 {
		t.Errorf("stored %d messages while the stream ran and %d while the tool ran, want none",
			storedMidStream, storedDuringTool)
	}
	if len(store.msgs) != 1 {
		t.Fatalf("stored %d messages, want 1", len(store.msgs))
	}
	if !slices.Equal(store.sessions, []message.SessionID{"ses_run"}) {
		t.Errorf("stored in sessions %v, want the run's ses_run", store.sessions)
	}
	msg := store.msgs[0]
	if msg.ID != turns[0].ID || msg.Role != message.RoleAgent || msg.Usage().InputTokens != 7 {
		t.Errorf("stored message = %+v, want the turn's agent message with its usage", msg)
	}
	if len(msg.Parts) != 3 {
		t.Fatalf("stored parts = %+v, want the text, the tool call and the usage", msg.Parts)
	}
	if _, ok := msg.Parts[2].(message.UsagePart); !ok {
		t.Errorf("stored part = %+v, want the usage after the tool call", msg.Parts[2])
	}
	tp, ok := msg.Parts[1].(message.ToolPart)
	if !ok {
		t.Fatalf("stored part = %+v, want the tool call", msg.Parts[1])
	}
	if done, ok := tp.State.(message.ToolStateCompleted); !ok || done.Output != "hi" {
		t.Errorf("stored tool state = %+v, want completed with its output", tp.State)
	}
}

// TestAgent_StoresStoppedResponse checks that a response stopped mid-stream
// is stored as far as it got, under a context the stop did not end.
func TestAgent_StoresStoppedResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &recordStore{}
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return func(yield func(agent.StreamPart, error) bool) {
				yield(agent.TextDelta{Text: "hel"}, nil)
				cancel()
				yield(agent.TextDelta{Text: "lo"}, nil)
			}, nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
		Store:    store,
	}

	_, events, err := runAgent(t, ctx, ag)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(store.msgs) != 1 || len(store.msgs[0].Parts) != 1 {
		t.Fatalf("stored %+v, want one message with one part", store.msgs)
	}
	if tp, ok := store.msgs[0].Parts[0].(message.TextPart); !ok || tp.Text != "hel" {
		t.Errorf("stored part = %+v, want the partial text %q", store.msgs[0].Parts[0], "hel")
	}
	ended := eventsOfType[agent.MessageEnded](events)
	if len(ended) != 1 || !ended[0].Stopped || ended[0].Err != nil {
		t.Errorf("MessageEnded = %+v, want one that was stopped without an error", ended)
	}
}

// TestAgent_StoppedTurnRecordsItsStep checks that a stop does not keep a
// turn from ending its message with the workspace tree, since its tools may
// have changed files before it.
func TestAgent_StoppedTurnRecordsItsStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &recordStore{}
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return func(yield func(agent.StreamPart, error) bool) {
				yield(agent.TextDelta{Text: "hel"}, nil)
				cancel()
				yield(agent.TextDelta{Text: "lo"}, nil)
			}, nil
		}}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		TurnHooks: []agent.TurnHook{snapshotForAgentTest(t)},
		Store:     store,
	}

	if _, _, err := runAgent(t, ctx, ag); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(store.msgs) != 1 {
		t.Fatalf("stored %d messages, want 1", len(store.msgs))
	}
	parts := store.msgs[0].Parts
	if step, ok := parts[len(parts)-1].(snapshot.SnapshotPart); !ok || step.Tree.IsZero() {
		t.Errorf("stored parts = %+v, want them to end with a snapshot part holding a tree", parts)
	}
}

func TestAgent_MaxSteps(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.ToolCallResponse("call_1", "echo", `{}`), nil
		}}, "test"),
		Tools: newTools(agent.Tool{
			Name: "echo",
			Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
				return agent.ToolOutput{Text: "ok"}, nil
			},
		}),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("loop")},
		MaxSteps: 3,
	}

	turns, _, err := runAgent(t, context.Background(), ag)

	maxErr, ok := errors.AsType[*agent.MaxStepsError](err)
	if !ok {
		t.Fatalf("expected MaxStepsError, got %v", err)
	}
	if maxErr.Steps != 3 {
		t.Fatalf("expected 3 steps, got %d", maxErr.Steps)
	}
	if len(turns) != 3 {
		t.Fatalf("expected 3 completed turns, got %d", len(turns))
	}
}

// TestAgent_NoMaxSteps checks that a run with no MaxSteps goes on for as
// long as the model calls tools, with no limit of its own.
func TestAgent_NoMaxSteps(t *testing.T) {
	const calling = 40 // responses that call a tool before one that does not
	responses := 0
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			responses++
			if responses > calling {
				return agenttest.TextResponse("done"), nil
			}
			return agenttest.ToolCallResponse("call_1", "echo", `{}`), nil
		}}, "test"),
		Tools: newTools(agent.Tool{
			Name: "echo",
			Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
				return agent.ToolOutput{Text: "ok"}, nil
			},
		}),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("loop")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("run with no step limit: %v", err)
	}
	if len(turns) != calling+1 {
		t.Fatalf("completed %d turns, want %d", len(turns), calling+1)
	}
}

// TestAgent_NilOnEventIsSafe checks that event handling is optional.
func TestAgent_NilOnEventIsSafe(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.TextResponse("hi"), nil }}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
	}

	calls, err := ag.RunTurn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 || len(ag.Messages) != 2 || len(ag.Messages[1].Content) != 1 {
		t.Fatalf("calls = %d, history = %+v, want no calls and the reply appended with 1 part", calls, ag.Messages)
	}
}

// turnRecorder is an extension whose turn hook records the turns it is
// told of.
type turnRecorder struct{ turns []agent.Turn }

func (*turnRecorder) Name() string { return "turns" }

func (r *turnRecorder) OnTurnEnd(_ context.Context, t agent.Turn, _ agent.PartSink) {
	r.turns = append(r.turns, t)
}

// TestEndHooks_TellHowTheTurnEnded checks that a turn hook is told the
// failure that ended a turn, and that a stopped turn was stopped rather than
// failed, while it can still add to the message.
func TestEndHooks_TellHowTheTurnEnded(t *testing.T) {
	rec := &turnRecorder{}
	v := agent.Variant{Provider: "test", Model: "test-model"}
	a := &agent.Agent{SessionID: "ses_end", Variant: v, TurnHooks: []agent.TurnHook{rec}}
	s := agent.NewTurnState("msg_1", v, func(agent.Event) {})

	failed := errors.New("boom")
	a.EndHooks(t.Context(), s, failed)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a.EndHooks(ctx, s, context.Canceled)

	if len(rec.turns) != 2 {
		t.Fatalf("turn hook ran %d times, want 2", len(rec.turns))
	}
	if got := rec.turns[0]; got.Stopped || !errors.Is(got.Err, failed) || got.SessionID != "ses_end" || got.MessageID != "msg_1" || got.Model != v.String() {
		t.Errorf("failed turn = %+v, want its failure", got)
	}
	if got := rec.turns[1]; !got.Stopped || got.Err != nil {
		t.Errorf("stopped turn = %+v, want stopped with no failure", got)
	}
}
