// Package agent runs the model and tool loop of a run: it streams each model
// response into the parts of an agent message, runs the tool calls the
// model makes, lets its TurnHooks, such as the extensions that implement
// extension.TurnHook, end the message, and stores it, until a response makes
// no tool calls. It reports each message and part as it changes as an Event,
// which the app turns into the events its clients read.
//
// It defines the tools the model calls, too. A [Tool] pairs what the model
// sees of a tool with what runs it, and [Tools] holds the tools the
// model may call, by name, and runs each call the model makes, as a
// [ToolResult]. A call that fails, or a run that does, ends with a marker in
// brackets, as [ToolFailure] and [FailedToolRun] write it. The tools
// themselves come from extensions, such as the built-in tools of
// extension/tools.
//
// The package also defines the model, authorization, and streaming
// interfaces that providers implement, and Providers, which gives access to
// the providers of the catalog and their models. The providers themselves
// come from extensions, through extension.DialHook, such as the ones of
// extension/protocol.
//
// It stores provider credentials and runs provider login, too. A provider
// can have several accounts, each identified by a UUID, one of which is
// active. The accounts saved by login live in auth.json, and an API key set
// in the configuration or environment appears as the read-only account
// [ConfigAccount]. [Accounts] resolves the credential of every provider
// request from scratch, from the active account. It keeps no credential in
// memory, so a login, logout, account switch, key change, or token refresh
// made by any process applies to the next request of every process.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// Agent runs a model and tool loop. It is not safe for concurrent use.
type Agent struct {
	// Providers sends model requests.
	Providers *Providers
	// Tools executes requested tools.
	Tools *Tools
	// Variant identifies the model, as it is to run: its provider, mode and
	// reasoning effort.
	Variant Variant
	// Messages contains provider history. Each turn that completes appends
	// its agent message.
	Messages []Message
	// ToolDefinitions contains definitions advertised to the model.
	ToolDefinitions []ToolDefinition

	// SessionID is the session the run belongs to, which names the prompt
	// cache of its requests, which turn hooks are told, and which Store
	// stores the run's messages in. It may be empty when Store is nil.
	SessionID message.SessionID

	// OnEvent receives events synchronously in emission order. Nil discards
	// events.
	OnEvent func(Event)

	// MaxSteps limits model responses per Run. Nonpositive values use the default.
	MaxSteps int

	// TurnHooks add parts to each turn's message as the turn ends, in
	// order.
	TurnHooks []TurnHook

	// RetryPolicy controls retries for transient provider failures.
	RetryPolicy Policy

	// Store stores each turn's agent message in session SessionID once
	// the turn ends, also when it fails or is stopped. Nil stores nothing.
	Store MessageStore
}

// MessageStore stores the messages of an agent's turns, as session.Store
// does. The agent stores a stopped turn's message too, so the context it
// passes is never canceled.
type MessageStore interface {
	// AppendMessage stores msg below the head of session sessionID.
	AppendMessage(ctx context.Context, sessionID message.SessionID, msg *message.Content) error
}

var (
	// ErrNotStored reports that a turn's message could not be stored.
	ErrNotStored = errors.New("message not stored")

	// ErrIncompleteToolInput reports a tool call whose input the provider
	// never ended, so it may have arrived in part or not at all.
	ErrIncompleteToolInput = errors.New("tool arguments did not arrive in full")
)

const defaultMaxSteps = 16

// MaxStepsError reports that Run reached its model response limit.
type MaxStepsError struct {
	// Steps is the configured response limit.
	Steps int
}

// Error returns the step-limit failure text.
func (e *MaxStepsError) Error() string {
	return fmt.Sprintf("agent loop exceeded %d steps", e.Steps)
}

// Run runs turns until one makes no tool calls, a turn fails, ctx ends, or the
// step limit is reached. Each invocation starts from the current Messages
// history. Once ctx ends, no further turn starts, and Run returns ctx's error.
func (a *Agent) Run(ctx context.Context) error {
	steps := a.MaxSteps
	if steps <= 0 {
		steps = defaultMaxSteps
	}
	for range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		calls, err := a.RunTurn(ctx)
		if err != nil {
			return err
		}
		if calls == 0 {
			return nil
		}
	}
	return &MaxStepsError{Steps: steps}
}

// RunTurn streams one model response, processes its tool calls, and reports
// how many calls it processed. A turn that completes appends its message to
// Messages; a failed one appends nothing.
//
// The message is opened with MessageStarted and closed with MessageEnded
// however the turn ends, and its parts arrive in between through the part
// events. With a Store, the message is stored when the turn
// ends. A failure to store ends the turn with that error.
func (a *Agent) RunTurn(ctx context.Context) (calls int, err error) {
	s := NewTurnState(message.NewID(), a.Variant, a.Emit)
	a.Emit(MessageStarted{Message: s.Message()})
	defer func() { err = a.EndTurn(ctx, s, err) }()

	stream, err := a.streamWithRetry(ctx, s.MessageID)
	if err != nil {
		return 0, err
	}
	if err := s.Consume(ctx, stream); err != nil {
		return 0, err
	}
	s.EndBlocks()
	return a.ExecTools(ctx, s), nil
}

// EndTurn ends the turn s holds, which err, if not nil, ended. It ends the
// parts still open, marking unfinished tool calls as interrupted, runs the
// turn hooks, stores the message if it has parts, emits MessageEnded with the
// final message, and appends a message that completed to Messages. A stored
// message ends with its parent and creation time. A message whose ctx ended
// was stopped, and has no error unless it could not be stored.
//
// It returns err joined with any failure to store, which wraps
// [ErrNotStored]. A stopped turn returns only the failure to store, since the
// stop itself is not an error.
func (a *Agent) EndTurn(ctx context.Context, s *TurnState, err error) error {
	s.EndBlocks()
	if err != nil || ctx.Err() != nil {
		s.Interrupt()
	}
	a.EndHooks(ctx, s, err)
	ev := MessageEnded{Message: s.Message(), Finish: s.Reason}
	var storeErr error
	if a.Store != nil && len(s.Parts) > 0 {
		msg := message.Content{
			ID:    s.MessageID,
			Role:  message.RoleAgent,
			Parts: s.Parts,
			Model: a.Variant.String(),
		}
		// The message is stored also when ctx ended, since the turn was
		// stopped then, and what it produced so far is kept.
		if storeErr = a.Store.AppendMessage(context.WithoutCancel(ctx), a.SessionID, &msg); storeErr != nil {
			storeErr = fmt.Errorf("%w: %w", ErrNotStored, storeErr)
		} else {
			ev.Message.ParentID, ev.Message.CreatedAt = msg.ParentID, msg.CreatedAt
		}
	}

	ev.Stopped = ctx.Err() != nil
	switch {
	case storeErr != nil && ev.Stopped:
		err = storeErr
	case storeErr != nil:
		err = errors.Join(err, storeErr)
	}
	if err != nil && (!ev.Stopped || storeErr != nil) {
		ev.Err = err
	}
	a.Emit(ev)

	if err == nil {
		a.Messages = append(a.Messages, Message{
			Role:     message.RoleAgent,
			Content:  s.Parts,
			Provider: a.Variant.Provider,
			Model:    a.Variant.Model,
		})
	}
	return err
}

// streamWithRetry opens the response stream for message id, retrying
// classified setup failures, each of which it reports as Retrying before it
// waits.
func (a *Agent) streamWithRetry(ctx context.Context, id message.ID) (StreamResponse, error) {
	maxAttempts := a.RetryPolicy.EffectiveMaxAttempts()
	delay := a.RetryPolicy.EffectiveDelay()

	var lastErr error
	for attempt := range maxAttempts {
		stream, err := a.Providers.Stream(ctx, a.Variant, Request{
			Messages: slices.Clone(a.Messages),
			Tools:    a.ToolDefinitions,
			CacheKey: string(a.SessionID),
		})
		if err == nil {
			return stream, nil
		}
		lastErr = err

		verdict := Classify(err)
		if !verdict.Retryable || attempt+1 >= maxAttempts {
			return nil, err
		}
		a.Emit(Retrying{MessageID: id, Attempt: attempt + 1, MaxAttempts: maxAttempts, Delay: delay, Reason: verdict.Message, Err: err})

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

// Emit reports one event to OnEvent, if a consumer is attached.
func (a *Agent) Emit(ev Event) {
	if a.OnEvent != nil {
		a.OnEvent(ev)
	}
}

// ExecTools executes pending calls in order, updating each call's part as it
// starts running and ending it when it finishes, and reports how many calls
// it processed. Each call retains one part ID across state changes. A call
// whose input the provider never ended fails without running, as it would
// act on part of its arguments.
func (a *Agent) ExecTools(ctx context.Context, s *TurnState) int {
	calls := 0
	for slot, tp := range s.PendingTools() {
		input := tp.Input()
		started := time.Now().UTC()
		if !s.Ended[tp.CallID] {
			slog.Error("run tool", "name", tp.Name, "call_id", tp.CallID, "err", ErrIncompleteToolInput)
			res := ToolFailure(tp.CallID, ErrIncompleteToolInput.Error())
			s.EndPart(slot, tp.WithState(ToolStateFor(res, input, started)))
			calls++
			continue
		}
		s.UpdatePart(slot, tp.WithState(message.ToolStateRunning{Input: input, StartedAt: started}))

		res := a.Tools.Execute(ctx, ToolCall{ID: tp.CallID, Name: tp.Name, Args: input})
		s.EndPart(slot, tp.WithState(ToolStateFor(res, input, started)))
		calls++
	}
	return calls
}

// ToolStateFor converts a tool result to a completed or failed state.
func ToolStateFor(res ToolResult, input json.RawMessage, started time.Time) message.ToolState {
	ended := time.Now().UTC()
	if res.IsError {
		return message.ToolStateError{
			Input:     input,
			Error:     res.Content,
			StartedAt: started,
			EndedAt:   ended,
		}
	}
	return message.ToolStateCompleted{
		Input:     input,
		Output:    res.Content,
		Title:     res.Title,
		StartedAt: started,
		EndedAt:   ended,
	}
}

// EndHooks lets each of TurnHooks add parts to the end of the turn's
// message, in order, telling them whether ctx, the run's
// context, ended, and err, the failure that ended the turn, if any. A stop
// does not keep the hooks from adding to the message, as it does not keep
// the message from being stored, so they run in a context that does not end
// with ctx.
func (a *Agent) EndHooks(ctx context.Context, s *TurnState, err error) {
	t := Turn{
		SessionID: a.SessionID,
		MessageID: s.MessageID,
		Model:     a.Variant.String(),
		Stopped:   ctx.Err() != nil,
	}
	if !t.Stopped {
		t.Err = err
	}
	hookCtx := context.WithoutCancel(ctx)
	for _, hook := range a.TurnHooks {
		hook.OnTurnEnd(hookCtx, t, s.Sink())
	}
}
