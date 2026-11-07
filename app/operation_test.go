package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/agenttest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/internal/catalog"
	"github.com/unaquatic/flow/internal/flock"
	"github.com/unaquatic/flow/internal/pubsub"
)

// newTools returns Tools holding tools.
func newTools(tools ...agent.Tool) *agent.Tools {
	s := agent.NewTools()
	for _, t := range tools {
		s.Register(t)
	}
	return s
}

// eventsOfType returns events with concrete type E.
func eventsOfType[E Event](events []Event) []E {
	var out []E
	for _, ev := range events {
		if typed, ok := ev.(E); ok {
			out = append(out, typed)
		}
	}
	return out
}

// newActorTestApp returns an isolated app with a stubbed text stream.
func newActorTestApp(t *testing.T, streamText string) (*App, *agenttest.Gateway) {
	t.Helper()
	db, project := openTestDatabase(t, t.TempDir())
	gw := &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return agenttest.TextResponse(streamText), nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(`{"model":"mock/test-model"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return &App{
		sessions:   openSessions(t, db, project),
		db:         db,
		providers:  agenttest.NewProviders(t, gw),
		tools:      agent.NewTools(),
		extensions: []extension.Extension{newDisabledSnapshot(t)},
		config:     config.NewService(home),
		broker:     pubsub.NewBroker[Event](),
		lockDir:    t.TempDir(),
		workerCtx:  ctx,
		cancel:     cancel,
		operations: make(map[message.SessionID]*Operation),
	}, gw
}

// send starts a send on session id, or fails the test.
func send(t *testing.T, a *App, id message.SessionID, input SendInput) {
	t.Helper()
	if err := a.Session(id).Send(input); err != nil {
		t.Fatalf("send to %s: %v", id, err)
	}
}

// collectUntilIdle collects events through EventAgentEnded.
func collectUntilIdle(t *testing.T, events <-chan Event) []Event {
	t.Helper()
	var out []Event
	for {
		select {
		case ev := <-events:
			out = append(out, ev)
			if ev.EventType() == TypeAgentEnded {
				return out
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for the run to end; got %d events", len(out))
		}
	}
}

// agentEnds returns the EventMessageEnded of each agent message in
// events.
func agentEnds(events []Event) []EventMessageEnded {
	var out []EventMessageEnded
	for _, e := range eventsOfType[EventMessageEnded](events) {
		if e.Message.Role == message.RoleAgent {
			out = append(out, e)
		}
	}
	return out
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, ev := range events {
		out[i] = ev.EventType()
	}
	return out
}

func TestSend_StreamsAndPersists(t *testing.T) {
	a, _ := newActorTestApp(t, "hello there")
	events := a.Subscribe(t.Context())

	send(t, a, "ses_actor", SendInput{Text: "hi", MessageID: "msg_user1"})
	got := collectUntilIdle(t, events)

	types := eventTypes(got)
	for _, want := range []EventType{
		TypeAgentStarted, TypeMessageStarted, TypePartStarted, TypePartEnded, TypeMessageEnded, TypeAgentEnded,
	} {
		if !slices.Contains(types, want) {
			t.Errorf("missing event %q in %v", want, types)
		}
	}

	// The run starts, then the prompt starts with its text, and ends once
	// stored, with its creation time. Snapshots are off, so no part event
	// comes between.
	if len(got) < 3 {
		t.Fatalf("got %d events, want the run's start and the prompt's start and end first", len(got))
	}
	if _, ok := got[0].(EventAgentStarted); !ok {
		t.Errorf("first event = %+v, want the start of the run", got[0])
	}
	started, ok := got[1].(EventMessageStarted)
	if !ok || started.Message.ID != "msg_user1" || started.Message.Role != message.RoleUser || started.SessionID != "ses_actor" {
		t.Fatalf("second event = %+v, want the start of user message msg_user1 in ses_actor", got[1])
	}
	if !started.Message.CreatedAt.IsZero() {
		t.Errorf("prompt started with creation time %v, want none before it is stored", started.Message.CreatedAt)
	}
	ended, ok := got[2].(EventMessageEnded)
	if !ok || ended.Message.ID != started.Message.ID || !reflect.DeepEqual(ended.Message.Parts, started.Message.Parts) ||
		ended.Message.CreatedAt.IsZero() || ended.Error != nil {
		t.Errorf("third event = %+v, want the end of the same prompt, stored", got[2])
	}
	if tp, ok := started.Message.Parts[0].(message.TextPart); !ok || tp.Text != "hi" || tp.ID == "" {
		t.Errorf("prompt parts = %+v, want the text with a part ID", started.Message.Parts)
	}

	if a.Session("ses_actor").Busy() {
		t.Error("session still streaming after its run ended")
	}

	msgs := branchOf(t, a.Sessions(), "ses_actor")
	if len(msgs) != 2 {
		t.Fatalf("persisted %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != message.RoleUser || msgs[1].Role != message.RoleAgent {
		t.Errorf("roles = %v, %v; want user, agent", msgs[0].Role, msgs[1].Role)
	}
}

func TestSend_DerivesHistoryFromLog(t *testing.T) {
	a, gw := newActorTestApp(t, "reply")
	appendMessage(t, a.sessions, "ses_hist", &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "earlier"}},
	})

	events := a.Subscribe(t.Context())
	send(t, a, "ses_hist", SendInput{Text: "again"})
	collectUntilIdle(t, events)

	// Provider history includes the persisted message and new prompt.
	if len(gw.StreamCalls) != 1 {
		t.Fatalf("gateway saw %d stream calls, want 1", len(gw.StreamCalls))
	}
	if got := len(gw.StreamCalls[0]); got != 2 {
		t.Fatalf("stream history has %d messages, want 2", got)
	}

	// The next send rebuilds the expanded persisted history.
	send(t, a, "ses_hist", SendInput{Text: "once more"})
	collectUntilIdle(t, events)
	if got := len(gw.StreamCalls[1]); got != 4 {
		t.Fatalf("second stream history has %d messages, want 4", got)
	}
}

// TestSend_KeepsReasoningOfTheModelThatRuns checks that history rebuilt from
// the log keeps the reasoning of the replies that the model running now
// wrote, in any mode and at any effort, and drops that of the replies another
// model wrote, as the variant each message names tells them apart.
func TestSend_KeepsReasoningOfTheModelThatRuns(t *testing.T) {
	a, gw := newActorTestApp(t, "hello")
	dir := t.TempDir()
	gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, variantDoc)})
	a.providers = agent.NewProviders(agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: dir})}, agenttest.Provider{ID: providerA, Gateway: gw})
	stored := func(role message.Role, model string, parts ...message.ContentPart) {
		appendMessage(t, a.sessions, "ses_mixed", &message.Content{ID: message.NewID(), Role: role, Model: model, Parts: parts})
	}
	stored(message.RoleUser, "provider-a/model-a:fast:high", message.TextPart{Text: "hi"})
	stored(message.RoleAgent, "provider-a/model-a:fast:high", message.ReasoningPart{Text: "own"}, message.TextPart{Text: "one"})
	stored(message.RoleUser, "provider-b/model-x", message.TextPart{Text: "and you?"})
	stored(message.RoleAgent, "provider-b/model-x", message.ReasoningPart{Text: "other"}, message.TextPart{Text: "two"})

	events := a.Subscribe(t.Context())
	send(t, a, "ses_mixed", SendInput{Text: "again", Model: "provider-a/model-a:xhigh"})
	collectUntilIdle(t, events)

	if len(gw.StreamCalls) != 1 {
		t.Fatalf("gateway saw %d stream calls, want 1", len(gw.StreamCalls))
	}
	var thoughts []string
	for _, m := range gw.StreamCalls[0] {
		for _, p := range m.Content {
			if r, ok := p.(message.ReasoningPart); ok {
				thoughts = append(thoughts, r.Text)
			}
		}
	}
	if !slices.Equal(thoughts, []string{"own"}) {
		t.Errorf("reasoning sent = %q, want only the running model's", thoughts)
	}
}

// toolState returns the state of the only tool part of msg.
func toolState(t *testing.T, msg message.Content) message.ToolState {
	t.Helper()
	var states []message.ToolState
	for _, p := range msg.Parts {
		if tp, ok := p.(message.ToolPart); ok {
			states = append(states, tp.State)
		}
	}
	if len(states) != 1 {
		t.Fatalf("message %s has %d tool parts, want 1", msg.ID, len(states))
	}
	return states[0]
}

// TestSend_StoresResponseWhenTurnEnds checks that a response is stored
// once its turn ends, after its tools have run, and that the prompt and each
// response end with a step part recording the workspace.
func TestSend_StoresResponseWhenTurnEnds(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	a.extensions = []extension.Extension{snapshotForAgentTest(t)}
	calls := 0
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		calls++
		if calls == 1 {
			return agenttest.ToolCallResponse("call_1", "echo", `{"text":"hi"}`), nil
		}
		return agenttest.TextResponse("done"), nil
	}})
	var during []message.Content
	var duringErr error
	a.tools = newTools(agent.Tool{
		Name: "echo",
		Execute: func(ctx context.Context, _ map[string]any) (agent.ToolOutput, error) {
			during, duringErr = a.Sessions().Branch(ctx, "ses_store")
			return agent.ToolOutput{Text: "echoed"}, nil
		},
	})

	events := a.Subscribe(t.Context())
	send(t, a, "ses_store", SendInput{Text: "hi"})
	got := collectUntilIdle(t, events)

	if duringErr != nil {
		t.Fatal(duringErr)
	}
	if len(during) != 1 {
		t.Fatalf("while the tool ran the session held %d messages, want only the prompt", len(during))
	}

	after := branchOf(t, a.Sessions(), "ses_store")
	if len(after) != 3 {
		t.Fatalf("stored %d messages, want the prompt and two responses", len(after))
	}
	if done, ok := toolState(t, after[1]).(message.ToolStateCompleted); !ok || done.Output != "echoed" {
		t.Errorf("stored tool state = %+v, want completed with the output", toolState(t, after[1]))
	}
	for _, m := range after {
		if parts := m.Parts; len(parts) == 0 {
			t.Errorf("stored %s message has no parts", m.Role)
		} else if step, ok := parts[len(parts)-1].(snapshot.StepPart); !ok || step.Snapshot.IsZero() {
			t.Errorf("stored %s message ends with %+v, want a step part holding a tree", m.Role, parts[len(parts)-1])
		}
	}

	ended := agentEnds(got)
	if len(ended) != 2 || ended[0].Message.ID != after[1].ID {
		t.Fatalf("EventMessageEnded = %+v, want one per response", ended)
	}
	if e := ended[0]; e.Finish != agent.StopToolUse || e.Stopped || e.Error != nil {
		t.Errorf("first response ended with %+v, want finished for tool use", e)
	}
}

// TestSend_PromptSnapshotEvents checks that a prompt's step part, which the
// snapshot extension adds, arrives through the part events between the
// prompt's start and its end: pending first when recording the workspace
// takes longer than the extension's IndexingDelay, as a delay of zero makes
// it, and with its tree at once when recording is quicker. The prompt ends
// stored, with the part's tree.
func TestSend_PromptSnapshotEvents(t *testing.T) {
	for _, tt := range []struct {
		name    string
		delay   time.Duration
		pending bool
	}{
		{name: "slow", delay: 0, pending: true},
		{name: "quick", delay: time.Hour, pending: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := newActorTestApp(t, "done")
			snaps := snapshotForAgentTest(t)
			snaps.IndexingDelay = tt.delay
			a.extensions = []extension.Extension{snaps}
			events := a.Subscribe(t.Context())
			send(t, a, "ses_snap", SendInput{Text: "hi", MessageID: "msg_prompt"})

			var types []EventType
			var steps []snapshot.StepPart
			var ended EventMessageEnded
			for _, ev := range collectUntilIdle(t, events) {
				switch e := ev.(type) {
				case EventMessageStarted:
					if e.Message.ID == "msg_prompt" {
						types = append(types, e.EventType())
					}
				case EventPartStarted:
					if e.MessageID == "msg_prompt" {
						types, steps = append(types, e.EventType()), append(steps, e.Part.(snapshot.StepPart))
					}
				case EventPartEnded:
					if e.MessageID == "msg_prompt" {
						types, steps = append(types, e.EventType()), append(steps, e.Part.(snapshot.StepPart))
					}
				case EventMessageEnded:
					if e.Message.ID == "msg_prompt" {
						types, ended = append(types, e.EventType()), e
					}
				}
			}
			if want := []EventType{TypeMessageStarted, TypePartStarted, TypePartEnded, TypeMessageEnded}; !slices.Equal(types, want) {
				t.Fatalf("prompt events = %v, want %v", types, want)
			}
			started, last := steps[0], steps[1]
			if started.Pending != tt.pending || started.Pending == !started.Snapshot.IsZero() {
				t.Errorf("step started as %+v, want pending = %v, with a tree unless pending", started, tt.pending)
			}
			if last.Pending || last.Snapshot.IsZero() || last.ID != started.ID {
				t.Errorf("step ended as %+v, want the part %q with its tree", last, started.ID)
			}
			if ended.Message.CreatedAt.IsZero() || snapshot.TreeIn(ended.Message.Parts) != last.Snapshot {
				t.Errorf("prompt ended as %+v, want it stored with the step's tree", ended.Message)
			}
		})
	}
}

// TestSend_FailedRunEndsWithError checks that a failed run and its message
// end with the failure.
func TestSend_FailedRunEndsWithError(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return nil, errors.New("dial failed")
	}})

	events := a.Subscribe(t.Context())
	send(t, a, "ses_fail", SendInput{Text: "hi"})

	got := collectUntilIdle(t, events)
	run := got[len(got)-1].(EventAgentEnded)
	if run.Error == nil || !strings.Contains(run.Error.Message, "dial failed") {
		t.Errorf("EventAgentEnded error = %+v, want the dial failure", run.Error)
	}
	ended := agentEnds(got)
	if len(ended) != 1 || ended[0].Error == nil || ended[0].Stopped {
		t.Errorf("EventMessageEnded = %+v, want one with the failure", ended)
	}
}

// TestSend_StoppedRunEndsWithoutError checks that a stopped run and its
// message end without an error, the message marked as stopped.
func TestSend_StoppedRunEndsWithoutError(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	release := make(chan struct{})
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return func(yield func(agent.StreamPart, error) bool) {
			if !yield(agent.TextDelta{Text: "partial"}, nil) {
				return
			}
			<-release
			yield(agent.TextDelta{Text: " more"}, nil)
		}, nil
	}})

	events := a.Subscribe(t.Context())
	send(t, a, "ses_stop", SendInput{Text: "hi"})
	for ev := range events {
		if _, ok := ev.(EventPartStarted); ok {
			break
		}
	}
	a.Session("ses_stop").Stop()
	close(release)

	got := collectUntilIdle(t, events)
	if run := got[len(got)-1].(EventAgentEnded); run.Error != nil {
		t.Errorf("EventAgentEnded error = %+v, want none", run.Error)
	}
	ended := agentEnds(got)
	if len(ended) != 1 || !ended[0].Stopped || ended[0].Error != nil {
		t.Errorf("EventMessageEnded = %+v, want one stopped without an error", ended)
	}
}

// TestSend_ShutdownStoresTheStreamingMessage checks that a message still
// streaming when the app shuts down is stored as far as it got.
func TestSend_ShutdownStoresTheStreamingMessage(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	release := make(chan struct{})
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return func(yield func(agent.StreamPart, error) bool) {
			if !yield(agent.TextDelta{Text: "partial"}, nil) {
				return
			}
			<-release
			yield(agent.TextDelta{Text: " more"}, nil)
		}, nil
	}})

	events := a.Subscribe(t.Context())
	send(t, a, "ses_shutdown", SendInput{Text: "hi"})
	for ev := range events {
		if _, ok := ev.(EventPartStarted); ok {
			break
		}
	}
	a.cancel()
	close(release)
	a.wg.Wait()

	msgs := branchOf(t, a.Sessions(), "ses_shutdown")
	if len(msgs) != 2 {
		t.Fatalf("stored %d messages, want the prompt and the partial reply", len(msgs))
	}
	if parts := msgs[1].Parts; len(parts) != 1 {
		t.Errorf("stored reply parts = %+v, want the partial text", parts)
	} else if tp, ok := parts[0].(message.TextPart); !ok || tp.Text != "partial" {
		t.Errorf("stored reply part = %+v, want the text partial", parts[0])
	}
}

// TestSend_StopDuringToolArgumentsInterruptsTheCall checks that a stop while
// a call's arguments stream stores the call as interrupted, with the
// arguments it had, and that the next request sends it as an interrupted
// result with empty arguments.
func TestSend_StopDuringToolArgumentsInterruptsTheCall(t *testing.T) {
	a, gw := newActorTestApp(t, "done")
	release := make(chan struct{})
	gw.StreamFunc = func() (agent.StreamResponse, error) {
		if len(gw.StreamCalls) > 1 {
			return agenttest.TextResponse("done"), nil
		}
		return func(yield func(agent.StreamPart, error) bool) {
			if !yield(agent.ToolUseStart{ID: "call_1", Name: "bash"}, nil) ||
				!yield(agent.ToolUseDelta{ID: "call_1", JSON: `{"command":"go te`}, nil) {
				return
			}
			<-release
			yield(agent.ToolUseDelta{ID: "call_1", JSON: `st"}`}, nil)
		}, nil
	}

	events := a.Subscribe(t.Context())
	send(t, a, "ses_args", SendInput{Text: "run the tests"})
	// The call starts, then its first argument chunk updates it.
	for ev := range events {
		if _, ok := ev.(EventPartUpdated); ok {
			break
		}
	}
	a.Session("ses_args").Stop()
	close(release)
	got := collectUntilIdle(t, events)

	want := message.ToolStateError{Input: json.RawMessage(`{"command":"go te`), Error: message.InterruptedError}
	ended := agentEnds(got)
	if len(ended) != 1 || !reflect.DeepEqual(toolState(t, message.Content{Parts: ended[0].Message.Parts}), want) {
		t.Fatalf("EventMessageEnded = %+v, want the call interrupted", ended)
	}
	stored := branchOf(t, a.Sessions(), "ses_args")
	if len(stored) != 2 || !reflect.DeepEqual(toolState(t, stored[1]), want) {
		t.Fatalf("stored %+v, want the prompt and the interrupted call", stored)
	}

	send(t, a, "ses_args", SendInput{Text: "again"})
	collectUntilIdle(t, events)
	history := gw.StreamCalls[1]
	var use message.ToolUsePart
	var result message.ToolResultPart
	for _, m := range history {
		for _, p := range m.Content {
			switch v := p.(type) {
			case message.ToolUsePart:
				use = v
			case message.ToolResultPart:
				result = v
			}
		}
	}
	if string(use.Input) != "{}" || !result.IsError || result.Text != message.InterruptedError {
		t.Errorf("sent call %+v with result %+v, want empty arguments and an interrupted result", use, result)
	}
}

// TestSessionBusy holds a session's lock from another open file, as another
// process would. Every operation that changes the session fails with
// ErrSessionBusy until the lock is released, and then succeeds.
func TestSessionBusy(t *testing.T) {
	a, _ := newActorTestApp(t, "hello")
	const id = "ses_busy"
	// A stored session is listed, so its status reports the other holder.
	appendMessage(t, a.sessions, id, &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "earlier"}},
	})
	path, err := a.sessionLockPath(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := flock.Lock(t.Context(), other, true); err != nil {
		t.Fatal(err)
	}
	events := a.Subscribe(t.Context())

	if _, err := a.Session(id).Undo(); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("Undo error = %v, want ErrSessionBusy", err)
	}
	if err := a.Session(id).DeleteMessage("m1"); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("DeleteMessage error = %v, want ErrSessionBusy", err)
	}
	if err := a.Session(id).Send(SendInput{Text: "hi"}); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("Send error = %v, want ErrSessionBusy", err)
	}
	if msgs := branchOf(t, a.Sessions(), id); len(msgs) != 1 {
		t.Errorf("busy send left %d messages, want only the earlier one", len(msgs))
	}
	if busy, err := a.BusySessions(t.Context()); err != nil || !busy[id] {
		t.Errorf("BusySessions = %v, %v; want %s busy", busy, err, id)
	}
	if err := a.DeleteSession(t.Context(), id); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("DeleteSession error = %v, want ErrSessionBusy", err)
	}

	if err := flock.Unlock(other); err != nil {
		t.Fatal(err)
	}
	send(t, a, id, SendInput{Text: "hi"})
	got := collectUntilIdle(t, events)
	if end := got[len(got)-1].(EventAgentEnded); end.Error != nil {
		t.Fatalf("send after release ended with %+v", end.Error)
	}
	if busy, err := a.BusySessions(t.Context()); err != nil || busy[id] {
		t.Errorf("BusySessions = %v, %v; want %s idle", busy, err, id)
	}
	if err := a.DeleteSession(t.Context(), id); err != nil {
		t.Fatalf("DeleteSession after release: %v", err)
	}
}

// TestSession_OneOperationAtATime checks that while a run holds a session,
// every other operation on it fails with ErrBusy and changes nothing, that
// Wait returns once the stopped run has let go, and that the session then
// takes the next operation.
func TestSession_OneOperationAtATime(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	release := make(chan struct{})
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return func(yield func(agent.StreamPart, error) bool) {
			if yield(agent.TextDelta{Text: "partial"}, nil) {
				<-release
			}
		}, nil
	}})
	const id = "ses_one"
	s := a.Session(id)
	events := a.Subscribe(t.Context())
	send(t, a, id, SendInput{Text: "first"})
	if !s.Busy() {
		t.Fatal("the run does not hold the session")
	}
	// Wait for the reply to stream, so that the stop below leaves one stored.
	for ev := range events {
		if _, ok := ev.(EventPartStarted); ok {
			break
		}
	}

	if err := s.Send(SendInput{Text: "second"}); !errors.Is(err, ErrBusy) {
		t.Errorf("Send error = %v, want ErrBusy", err)
	}
	if _, err := s.Undo(); !errors.Is(err, ErrBusy) {
		t.Errorf("Undo error = %v, want ErrBusy", err)
	}
	if err := s.DeleteMessage("m1"); !errors.Is(err, ErrBusy) {
		t.Errorf("DeleteMessage error = %v, want ErrBusy", err)
	}
	if busy, err := a.BusySessions(t.Context()); err != nil || !busy[id] {
		t.Errorf("BusySessions = %v, %v; want %s busy", busy, err, id)
	}

	s.Stop()
	close(release)
	if err := s.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if s.Busy() {
		t.Error("the stopped run still holds the session after Wait")
	}
	collectUntilIdle(t, events)
	if msgs := branchOf(t, a.Sessions(), id); len(msgs) != 2 {
		t.Fatalf("stored %d messages, want the first prompt and its reply only", len(msgs))
	}

	send(t, a, id, SendInput{Text: "second"})
	collectUntilIdle(t, events)
	if msgs := branchOf(t, a.Sessions(), id); len(msgs) != 4 {
		t.Errorf("stored %d messages, want both prompts and their replies", len(msgs))
	}
}

// TestClose_StopsRunsAndRefusesNewOnes checks that Close stops a run in
// progress and returns only once the run has let go of its session, and that
// no operation starts afterwards.
func TestClose_StopsRunsAndRefusesNewOnes(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	// The stream never ends by itself, so only the stop that Close makes
	// ends the run.
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return func(yield func(agent.StreamPart, error) bool) {
			for yield(agent.TextDelta{Text: "."}, nil) {
				time.Sleep(time.Millisecond)
			}
		}, nil
	}})
	const id = "ses_close"
	events := a.Subscribe(t.Context())
	send(t, a, id, SendInput{Text: "hi"})
	for ev := range events {
		if _, ok := ev.(EventPartStarted); ok {
			break
		}
	}

	a.Close()
	if op := a.Operation(id); op != nil {
		t.Error("the run still holds the session after Close")
	}
	s := a.Session(id)
	if err := s.Send(SendInput{Text: "again"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Send after Close error = %v, want ErrClosed", err)
	}
	if _, err := s.Undo(); !errors.Is(err, ErrClosed) {
		t.Errorf("Undo after Close error = %v, want ErrClosed", err)
	}
}

// TestSession_Stream checks that Stream runs a send in the caller: it yields
// the run's events in order, from its start to its end, while subscribers
// receive them too, and returns once the run has let go of the session.
func TestSession_Stream(t *testing.T) {
	a, _ := newActorTestApp(t, "hello")
	const id = "ses_stream"
	s := a.Session(id)
	events := a.Subscribe(t.Context())

	var types []EventType
	for ev, err := range s.Stream(SendInput{Text: "hi"}) {
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if ev.Base().SessionID != id {
			t.Errorf("event %s is of session %q, want %q", ev.EventType(), ev.Base().SessionID, id)
		}
		types = append(types, ev.EventType())
	}

	if s.Busy() {
		t.Error("the run still holds the session after Stream returned")
	}
	if len(types) < 2 || types[0] != TypeAgentStarted || types[len(types)-1] != TypeAgentEnded {
		t.Fatalf("event types = %v, want the run's start first and its end last", types)
	}
	if !slices.Contains(types, TypePartStarted) {
		t.Errorf("event types = %v, want the reply's parts", types)
	}
	if got := eventTypes(collectUntilIdle(t, events)); !slices.Equal(got, types) {
		t.Errorf("subscriber received %v, want %v", got, types)
	}
	if msgs := branchOf(t, a.Sessions(), id); len(msgs) != 2 {
		t.Errorf("stored %d messages, want the prompt and its reply", len(msgs))
	}
}

// TestSession_StreamErrors checks that a run that cannot start yields only its
// error, and that a failed run ends with its end and then its error.
func TestSession_StreamErrors(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		a, _ := newActorTestApp(t, "unused")
		const id = "ses_busy_stream"
		op, _, err := a.Claim(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Release(id, op)

		var got []error
		for ev, err := range a.Session(id).Stream(SendInput{Text: "hi"}) {
			if ev != nil {
				t.Errorf("Stream yielded %s, want no event", ev.EventType())
			}
			got = append(got, err)
		}
		if len(got) != 1 || !errors.Is(got[0], ErrBusy) {
			t.Errorf("Stream yielded errors %v, want ErrBusy alone", got)
		}
	})

	t.Run("failed run", func(t *testing.T) {
		a, _ := newActorTestApp(t, "unused")
		a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return nil, errors.New("dial failed")
		}})

		var last Event
		var runErr error
		for ev, err := range a.Session("ses_fail_stream").Stream(SendInput{Text: "hi"}) {
			if err != nil {
				runErr = err
				break
			}
			last = ev
		}
		if runErr == nil || !strings.Contains(runErr.Error(), "dial failed") {
			t.Fatalf("Stream error = %v, want the dial failure", runErr)
		}
		if end, ok := last.(EventAgentEnded); !ok || end.Error == nil {
			t.Errorf("event before the error = %+v, want the run's end with its error", last)
		}
	})
}

// TestSession_StreamBreakStopsTheRun checks that leaving the loop early stops
// the run, and that the loop ends only once the run has let go of the
// session, with what it streamed stored.
func TestSession_StreamBreakStopsTheRun(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	// The stream never ends by itself, so only the stop ends the run.
	a.providers = agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		return func(yield func(agent.StreamPart, error) bool) {
			for yield(agent.TextDelta{Text: "."}, nil) {
				time.Sleep(time.Millisecond)
			}
		}, nil
	}})
	const id = "ses_break"
	s := a.Session(id)

	for ev, err := range s.Stream(SendInput{Text: "hi"}) {
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if _, ok := ev.(EventPartStarted); ok {
			break
		}
	}

	if s.Busy() {
		t.Error("the run still holds the session after the loop ended")
	}
	if msgs := branchOf(t, a.Sessions(), id); len(msgs) != 2 {
		t.Errorf("stored %d messages, want the prompt and the partial reply", len(msgs))
	}
}

// TestPlay_StoresItsTurns checks that a script holds the session like any
// run, answers its prompt with its own model and tools, and stores its
// prompt and the replies it streamed before Stop ended it, each with the
// script's model and its usage.
func TestPlay_StoresItsTurns(t *testing.T) {
	a, _ := newActorTestApp(t, "unused")
	const id = "ses_script"
	s := a.Session(id)
	events := a.Subscribe(t.Context())
	calls := 0
	gw := &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
		if calls++; calls == 1 {
			return agenttest.ToolCallResponse("call_1", "echo", `{"text":"hi"}`), nil
		}
		return agenttest.Stream(agent.TextDelta{Text: "done"}, agent.Usage{InputTokens: 9, OutputTokens: 2},
			agent.Stop{Reason: agent.StopEndTurn}), nil
	}}
	script := Script{
		Provider: agenttest.Provider{ID: "script", Gateway: gw},
		Variant:  agent.Variant{Provider: "script", Model: "player"},
		Tools: newTools(agent.Tool{Name: "echo", Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
			return agent.ToolOutput{Text: "hi"}, nil
		}}),
		Next: func(ctx context.Context, i int) (string, bool) {
			if i > 0 {
				<-ctx.Done()
				return "", false
			}
			return "play", true
		},
	}
	if err := s.Play(script); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(SendInput{Text: "hi"}); !errors.Is(err, ErrBusy) {
		t.Errorf("Send during the script error = %v, want ErrBusy", err)
	}

	var ended []EventMessageEnded
	for ev := range events {
		if e, ok := ev.(EventMessageEnded); ok && e.Message.Role == message.RoleAgent {
			if ended = append(ended, e); len(ended) == 2 {
				break
			}
		}
	}
	s.Stop()
	got := collectUntilIdle(t, events)
	if end := got[len(got)-1].(EventAgentEnded); end.Error != nil {
		t.Fatalf("the script ended with %+v", end.Error)
	}

	msgs := branchOf(t, a.Sessions(), id)
	if text, _ := message.LastPart[message.TextPart](msgs[0].Parts); len(msgs) != 3 || msgs[0].Role != message.RoleUser || text.Text != "play" {
		t.Fatalf("stored %d messages, want the script's prompt and its 2 replies", len(msgs))
	}
	for i, e := range ended {
		stored := msgs[i+1]
		if stored.ID != e.Message.ID || stored.Role != message.RoleAgent {
			t.Errorf("stored message %d = %s %s, want reply %s", i+1, stored.Role, stored.ID, e.Message.ID)
		}
		if e.Message.ParentID == nil || e.Message.CreatedAt.IsZero() {
			t.Errorf("reply %s ended without its parent and creation time", e.Message.ID)
		}
		if v := a.Models().ParseVariant(e.Message.Model); v.Provider != "script" || v.Model != "player" || stored.Model != e.Message.Model {
			t.Errorf("reply %s came from %s, stored %s; want script/player, stored with it", e.Message.ID, e.Message.Model, stored.Model)
		}
	}
	if u := ended[1].Message.Usage(); u.InputTokens != 9 || msgs[2].Usage() != u {
		t.Errorf("last reply usage = %+v, stored %+v; want the script model's, stored with it", u, msgs[2].Usage())
	}
}

// TestSend_RecordsVariant checks that a send records the variant of its
// model, with its option and the level it runs at, on the prompt and the
// reply, in their events and stored, and sends the provider's model ID
// alone to the provider. A model that names no level is recorded at the
// provider's default.
func TestSend_RecordsVariant(t *testing.T) {
	tests := []struct {
		model, want string
	}{
		{model: "provider-a/model-a:fast:high", want: "provider-a/model-a:fast:high"},
		{model: "provider-a/model-a:xhigh", want: "provider-a/model-a:xhigh"},
		{model: "provider-a/model-a", want: "provider-a/model-a:medium"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			a, gw := newActorTestApp(t, "hello")
			dir := t.TempDir()
			gatewaytest.WriteEntry(t, dir, gatewaytest.Entry{Fetched: time.Now(), Data: catalogDoc(t, variantDoc)})
			a.providers = agent.NewProviders(agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: dir})}, agenttest.Provider{ID: providerA, Gateway: gw})

			events := a.Subscribe(t.Context())
			send(t, a, "ses_variant", SendInput{Text: "hi", Model: tt.model})
			got := collectUntilIdle(t, events)

			if !slices.Equal(gw.StreamModels, []string{"model-a"}) {
				t.Errorf("provider got models %q, want model-a", gw.StreamModels)
			}
			ended := eventsOfType[EventMessageEnded](got)
			if len(ended) != 2 {
				t.Fatalf("got %d message ends, want the prompt's and the reply's", len(ended))
			}
			for _, e := range ended {
				if m := e.Message; m.Model != tt.want {
					t.Errorf("%v message ended as %s, want %s", m.Role, m.Model, tt.want)
				}
			}
			for _, m := range branchOf(t, a.Sessions(), "ses_variant") {
				if m.Model != tt.want {
					t.Errorf("stored %v message as %s, want %s", m.Role, m.Model, tt.want)
				}
			}
		})
	}
}
