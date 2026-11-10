package app

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/extension"
)

// maxAgentSteps limits model responses within one run.
const maxAgentSteps = 16

var (
	// ErrClosed reports that the app was closed before an operation started.
	ErrClosed = errors.New("app closed")

	// ErrNothingToContinue reports that a session's transcript is empty, so a
	// run has nothing to pick up from.
	ErrNothingToContinue = errors.New("nothing to continue")
)

// SendInput describes a user prompt sent to a session.
type SendInput struct {
	// Text is the user prompt.
	Text string
	// Model optionally overrides the configured model.
	Model string
	// MessageID optionally supplies the user message ID.
	MessageID message.ID
}

// FileDiff summarizes one file changed by a history move, as the
// extension.MoveHook of an extension reports it.
type FileDiff = extension.FileDiff

// MoveResult is what a history move changed: the files its extensions
// changed, and what they checked out, such as the hash of the workspace
// snapshot the move reset the workspace to.
type MoveResult = extension.MoveResult

// Operation is the operation in progress on one session, such as a send or an
// undo. A session has at most one at a time, and none while it is idle.
type Operation struct {
	// Cancel cancels the context Claim returned with the operation.
	Cancel context.CancelFunc
	// Done is closed once the operation has released its session.
	Done chan struct{}
}

// Claim records an operation on session id, and returns it with the context
// it runs in, derived from ctx, which Stop cancels. An operation that must
// run to its end, such as an undo, runs in another context instead. Claim
// reports ErrBusy when the session has an operation in progress, and
// ErrClosed once the app is closed. Release ends the operation, and Close
// waits for it.
func (a *App) Claim(ctx context.Context, id message.SessionID) (*Operation, context.Context, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workerCtx.Err() != nil {
		return nil, nil, ErrClosed
	}
	if _, busy := a.operations[id]; busy {
		return nil, nil, ErrBusy
	}
	op := &Operation{Done: make(chan struct{})}
	ctx, op.Cancel = context.WithCancel(ctx)
	a.operations[id] = op
	// Close cancels the app's context under a.mu before it waits, so this
	// Add comes first.
	a.wg.Add(1)
	return op, ctx, nil
}

// Release ends op, the operation Claim returned for session id, so that
// another can start, and closes op.Done.
func (a *App) Release(id message.SessionID, op *Operation) {
	a.mu.Lock()
	delete(a.operations, id)
	a.mu.Unlock()
	op.Cancel()
	close(op.Done)
	a.wg.Done()
}

// hold claims session id and takes its lock. It returns the context the
// holder runs in, derived from ctx, which Stop cancels, and release, which
// unlocks the session and ends the claim. It holds nothing, and reports
// ErrBusy, ErrClosed, ErrInvalidSessionID or ErrSessionBusy, when either
// step fails.
func (a *App) hold(ctx context.Context, id message.SessionID) (context.Context, func(), error) {
	op, opCtx, err := a.Claim(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	unlock, err := a.lockSession(ctx, id)
	if err != nil {
		a.Release(id, op)
		return nil, nil, err
	}
	return opCtx, func() {
		unlock()
		a.Release(id, op)
	}, nil
}

// Operation returns the operation in progress on session id, or nil when the
// session is idle.
func (a *App) Operation(id message.SessionID) *Operation {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.operations[id]
}

// Session is a handle on one session of an App. It holds no state of its
// own: the App tracks the operation in progress on each session, so a
// Session is free to make and to drop. Conversation history is rebuilt from
// persisted messages for each send. Each operation that changes the session
// holds the session's lock, so no other process changes it meanwhile.
// Obtain sessions through App.Session.
type Session struct {
	app *App
	id  message.SessionID
	// ctx is the context the session's operations run in, the app's by
	// default, which ends when the app closes.
	ctx context.Context
	// observe, when set, also receives each event the session publishes, in
	// the goroutine that publishes it.
	observe func(Event)
}

// Session returns a handle on session id, whose operations run in the app's
// context. It starts nothing.
func (a *App) Session(id message.SessionID) Session {
	return Session{app: a, id: id, ctx: a.workerCtx}
}

// ID returns the session's ID.
func (s Session) ID() message.SessionID { return s.id }

// Busy reports whether an operation is in progress on the session, such as a
// send or an undo.
func (s Session) Busy() bool {
	return s.app.Operation(s.id) != nil
}

// Send starts a run that stores input's prompt and answers it, and returns
// once the run holds the session. The run publishes EventAgentStarted, then
// the prompt's events, and ends with EventAgentEnded, published once it has
// released the session. Stop ends it from the start; a run stopped before its
// first turn stores the prompt and nothing more.
//
// Nothing is stored, and the error is ErrBusy, when the session has another
// operation in progress, ErrSessionBusy when another process is changing it,
// ErrInvalidSessionID for an ID that cannot name its lock file, or ErrClosed
// once the app is closed.
func (s Session) Send(input SendInput) error {
	return s.start(func(s Session, ctx context.Context) error { return s.processSend(ctx, input) })
}

// Continue starts a run like Send's that picks up from where the transcript
// ends, without a new prompt, answering with model, or the configured model
// when it is empty. The run sends the transcript as it stands, whatever it
// ends at: a prompt a failed or stopped run left unanswered, tool calls, with
// their results and those of calls a stop cut short, or a reply.
//
// The run's replies continue the transcript's last message, so continuing
// from a message that already has replies opens a branch beside them. It
// reports ErrNothingToContinue when the transcript is empty, and the errors
// Send does otherwise.
func (s Session) Continue(model string) error {
	if err := s.continuable(); err != nil {
		return err
	}
	return s.start(func(s Session, ctx context.Context) error { return s.processContinue(ctx, model) })
}

// ContinueStream runs a continue like Continue, but in the caller, as Stream
// runs a send.
func (s Session) ContinueStream(model string) iter.Seq2[Event, error] {
	if err := s.continuable(); err != nil {
		return func(yield func(Event, error) bool) { yield(nil, err) }
	}
	return s.stream(func(s Session, ctx context.Context) error { return s.processContinue(ctx, model) })
}

// continuable reports ErrNothingToContinue when the transcript is empty. It
// reads without holding the session, so the run checks again once it does.
func (s Session) continuable() error {
	history, err := s.historyFromLog()
	if err != nil {
		return err
	}
	if !Continues(history) {
		return ErrNothingToContinue
	}
	return nil
}

// Continues reports whether a run has anything to pick up from in history:
// a message other than the system messages that open it.
func Continues(history []agent.Message) bool {
	return slices.ContainsFunc(history, func(m agent.Message) bool { return m.Role != message.RoleSystem })
}

// start holds the session and runs work on it in a goroutine of its own, as
// run describes, returning once work holds the session. It reports the errors
// Send documents.
func (s Session) start(work func(Session, context.Context) error) error {
	ctx, release, err := s.app.hold(s.ctx, s.id)
	if err != nil {
		return err
	}
	s.app.wg.Go(func() { s.run(ctx, release, work) })
	return nil
}

// Stream runs input like Send, but in the caller, which it blocks until the
// run has ended and released the session. It yields each event of the run as
// it is published, from EventAgentStarted to EventAgentEnded; subscribers
// receive them too.
//
// An error ends the sequence: the error Send would return when the run
// cannot start, with no event before it, or the run's failure, after its
// EventAgentEnded. Stopping the iteration early stops the run, and the
// sequence returns once the run has ended.
func (s Session) Stream(input SendInput) iter.Seq2[Event, error] {
	return s.stream(func(s Session, ctx context.Context) error { return s.processSend(ctx, input) })
}

// stream holds the session and runs work on it in the caller, yielding the
// run's events, as Stream documents.
func (s Session) stream(work func(Session, context.Context) error) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		ctx, release, err := s.app.hold(s.ctx, s.id)
		if err != nil {
			yield(nil, err)
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stopped := false
		r := s
		r.observe = func(ev Event) {
			if !stopped && !yield(ev, nil) {
				stopped = true
				cancel()
			}
		}
		if err := r.run(ctx, release, work); err != nil && !stopped {
			yield(nil, err)
		}
	}
}

// run runs work on the session in ctx, the context hold returned with
// release, between EventAgentStarted and EventAgentEnded. It releases the
// session before it publishes EventAgentEnded, also when it panics, and
// returns the run's failure.
func (s Session) run(ctx context.Context, release func(), work func(Session, context.Context) error) error {
	log := s.Logger()
	log.Info("run started")
	var err error
	func() {
		defer release()
		s.publish(EventAgentStarted{EventBase: s.eventBase()})
		err = work(s, ctx)
	}()
	ev := EventAgentEnded{EventBase: s.eventBase()}
	if err != nil {
		e := ErrorOf(err)
		ev.Error = &e
		log.Warn("run ended", "err", err)
	} else {
		log.Info("run ended")
	}
	s.publish(ev)
	return err
}

// Logger returns the default logger with the session as its session
// attribute, which also writes its records to the session's log.
func (s Session) Logger() *slog.Logger {
	return slog.With("session", s.id)
}

// Stop cancels the send in progress. It returns at once, and does nothing
// when the session is idle or making a change, such as an undo, which runs
// to its end.
func (s Session) Stop() {
	if op := s.app.Operation(s.id); op != nil {
		op.Cancel()
	}
}

// Wait waits until the operation in progress, if any, has released the
// session, or until the session's context ends. Another operation may start
// as soon as it returns.
func (s Session) Wait() error {
	op := s.app.Operation(s.id)
	if op == nil {
		return nil
	}
	select {
	case <-op.Done:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// Undo moves the session to the state before its newest visible prompt.
func (s Session) Undo() (MoveResult, error) {
	return s.change(s.undo)
}

// Redo moves the session forward by one prompt and its run, towards the state
// it was last moved back from.
func (s Session) Redo() (MoveResult, error) {
	return s.change(s.redo)
}

// Checkout moves the session to the state before messageID. An empty
// messageID returns to the latest state.
func (s Session) Checkout(messageID message.ID) (MoveResult, error) {
	return s.change(func() (MoveResult, error) { return s.checkoutAt(messageID) })
}

// DeleteMessage removes messageID and every message below it from the
// session.
func (s Session) DeleteMessage(messageID message.ID) error {
	_, err := s.change(func() (MoveResult, error) {
		return MoveResult{}, s.app.sessions.DeleteMessage(s.ctx, s.id, messageID)
	})
	return err
}

// change runs op, an operation that changes the session, while holding the
// session, and republishes the transcript when op succeeds. op runs in the
// session's context rather than the one hold returns, so Stop cannot end it
// halfway. op does not run, and the error is ErrBusy, when the session has
// another operation in progress, ErrSessionBusy when another process is
// changing it, or ErrClosed once the app is closed.
func (s Session) change(op func() (MoveResult, error)) (MoveResult, error) {
	_, release, err := s.app.hold(s.ctx, s.id)
	if err != nil {
		return MoveResult{}, err
	}
	defer release()
	moved, err := op()
	if err != nil {
		return MoveResult{}, err
	}
	if err := s.emitSessionLoaded(); err != nil {
		slog.Error("load session", "session", s.id, "err", err)
	}
	return moved, nil
}

// historyFromLog rebuilds provider history from the session's active branch.
// Each message keeps the provider and model ID of the variant it names, as
// the catalog tells its mode and effort apart, so that its reasoning goes
// back only to that model; a message that names none keeps neither.
func (s Session) historyFromLog() ([]agent.Message, error) {
	msgs, err := s.app.sessions.Branch(s.ctx, s.id)
	if err != nil {
		return nil, err
	}
	return s.history(msgs), nil
}

// historyThrough rebuilds provider history from the session's root to
// message id, as historyFromLog rebuilds it from the active branch.
func (s Session) historyThrough(id message.ID) ([]agent.Message, error) {
	msgs, err := s.app.sessions.Path(s.ctx, s.id, id)
	if err != nil {
		return nil, err
	}
	return s.history(msgs), nil
}

// history returns msgs as provider history, as historyFromLog describes.
func (s Session) history(msgs []message.Content) []agent.Message {
	cat := s.app.Models().Catalog()
	out := make([]agent.Message, 0, len(msgs))
	for _, m := range msgs {
		pm := agent.Message{Role: m.Role, Content: m.Parts}
		if m.Model != "" {
			v := agent.ParseVariant(m.Model, cat)
			pm.Provider, pm.Model = v.Provider, v.Model
		}
		out = append(out, pm)
	}
	return out
}

// eventBase stamps an event with this session and the current time.
func (s Session) eventBase() EventBase {
	return EventBase{SessionID: s.id, Timestamp: time.Now().UTC()}
}

// publish sends an event to the app broker, and to the session's observer if
// it has one.
func (s Session) publish(ev Event) {
	s.app.broker.Publish(ev)
	if s.observe != nil {
		s.observe(ev)
	}
}

// emitSessionLoaded publishes the session's active branch. Branches the head
// does not select stay persisted and unsent.
func (s Session) emitSessionLoaded() error {
	msgs, err := s.Messages()
	if err != nil {
		return err
	}
	s.publish(EventSessionLoaded{EventBase: s.eventBase(), Messages: msgs})
	return nil
}

// processSend persists a prompt and runs the agent against rebuilt history
// until ctx, the run's context, ends. Cancellation settles without an error,
// unless it left a message unstored.
//
// A prompt sent while the head sits behind its branch forks the history: the
// prompt is stored below the head, and the messages that previously followed
// it stay on the branch left behind. A prompt that would open the history
// is stored below the system message emitSystem stores first.
func (s Session) processSend(ctx context.Context, snd SendInput) error {
	v, err := s.app.Models().Resolve(snd.Model)
	if err != nil {
		return err
	}
	if err := s.emitSystem(); err != nil {
		return err
	}
	history, err := s.historyFromLog()
	if err != nil {
		return err
	}
	history = append(history, agent.NewUserMessage(snd.Text))

	if err := s.emitPrompt(snd, v, s.app.extensions); err != nil {
		return err
	}
	return s.answer(ctx, history, v)
}

// processContinue runs the agent against the rebuilt history, with no new
// prompt, until ctx, the run's context, ends, settling as processSend does.
// It checks the history again, since it may have changed before the run held
// the session. A history of system messages alone has nothing to continue.
func (s Session) processContinue(ctx context.Context, model string) error {
	history, err := s.historyFromLog()
	if err != nil {
		return err
	}
	if !Continues(history) {
		return ErrNothingToContinue
	}
	v, err := s.app.Models().Resolve(model)
	if err != nil {
		return err
	}
	return s.answer(ctx, history, v)
}

// answer runs the agent against history with model variant v until ctx,
// the run's context, ends. Cancellation settles without an error, unless it
// left a message unstored.
func (s Session) answer(ctx context.Context, history []agent.Message, v agent.Variant) error {
	return settle(ctx, s.runAgent(ctx, history, v))
}

// settle returns the error that a run which ctx, the run's context, ran and
// which ended with err reports: none for a stop, which is not a failure,
// unless it left a message unstored.
func settle(ctx context.Context, err error) error {
	if err == nil || (ctx.Err() != nil && !errors.Is(err, agent.ErrNotStored)) {
		return nil
	}
	return err
}

// emitPrompt persists the prompt opening a run, and publishes it as a reply
// is published: it starts with its text, the parts that the extension.PromptHook
// of each of exts adds, such as the workspace snapshot the run's first reply
// starts from, arrive through the part events, and it ends once stored, with
// its parent, creation time and branch position. The position is the
// prompt's own, because storing it below a moved head is what creates a
// fork. Like storing the prompt, the hooks run in the session's context, so
// a stop does not cut them short. A prompt that cannot be stored ends with
// the failure. The first prompt of a session stores the session too, which
// it then publishes as EventSessionUpdated. Each stored prompt then has the
// extensions title the session, as startTitling describes.
func (s Session) emitPrompt(snd SendInput, v agent.Variant, exts []extension.Extension) error {
	id := snd.MessageID
	if id == "" {
		id = message.NewID()
	}
	msg := message.Content{
		ID:    id,
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{ID: message.NewPartID(), Text: snd.Text}},
		Model: v.String(),
	}
	prompt := func() Message {
		m := msg
		m.Parts = slices.Clone(msg.Parts)
		return Message{Content: m}
	}
	change := func(part message.ContentPart) PartChange {
		return PartChange{EventBase: s.eventBase(), MessageID: id, Part: part}
	}
	s.publish(EventMessageStarted{EventBase: s.eventBase(), Message: prompt()})
	sink := agent.PartSink{
		Start: func(part message.ContentPart) { s.publish(EventPartStarted{change(part)}) },
		End: func(part message.ContentPart) {
			msg.Parts = append(msg.Parts, part)
			s.publish(EventPartEnded{change(part)})
		},
	}
	p := extension.Prompt{SessionID: s.id, MessageID: id, Text: snd.Text, Model: msg.Model}
	for hook := range extension.Hooks[extension.PromptHook](exts) {
		hook.OnPrompt(s.ctx, p, sink)
	}
	stored, err := s.app.sessions.Stored(s.ctx, s.id)
	if err == nil {
		err = s.app.sessions.AppendMessage(s.ctx, s.id, &msg)
	}
	if err != nil {
		e := NewUnknownError(err.Error())
		s.publish(EventMessageEnded{EventBase: s.eventBase(), Message: prompt(), Error: &e})
		return err
	}
	s.Logger().Info("prompt stored", "message", id, "model", msg.Model)
	at, err := s.branchPositions([]message.Content{msg})
	ended := prompt()
	ended.Branch = at[id]
	s.publish(EventMessageEnded{EventBase: s.eventBase(), Message: ended})
	if !stored {
		if err := s.publishStored(); err != nil {
			s.Logger().Warn("read the session the prompt stored", "err", err)
		}
	}
	s.startTitling(p)
	return err
}

// runAgent runs the model and tool loop, with the retry settings the config
// file has as it starts, and fails when the file cannot be read. The agent
// stores each agent message when its turn ends.
func (s Session) runAgent(ctx context.Context, history []agent.Message, v agent.Variant) error {
	cfg, err := s.app.config.Get()
	if err != nil {
		return err
	}
	run := &agent.Agent{
		Providers:       s.app.providers,
		Tools:           s.app.tools,
		Variant:         v,
		Messages:        history,
		ToolDefinitions: s.app.tools.WireTools(),
		MaxSteps:        maxAgentSteps,
		TurnHooks:       turnHooks(s.app.extensions),
		SessionID:       s.id,
		OnEvent:         s.publishAgent,
		RetryPolicy: agent.Policy{
			MaxAttempts: cfg.Agent.RetryMaxAttempts,
			Delay:       cfg.Agent.RetryDelay.Duration,
		},
		// The agent stores a stopped run's messages too, which the database
		// is still open for: the app closes it only once its runs return.
		Store: s.app.sessions,
	}

	return run.Run(ctx)
}

// turnHooks returns the extensions among exts that implement
// extension.TurnHook, in order, as the agent takes them.
func turnHooks(exts []extension.Extension) []agent.TurnHook {
	var hooks []agent.TurnHook
	for h := range extension.Hooks[extension.TurnHook](exts) {
		hooks = append(hooks, h)
	}
	return hooks
}

// The session store is the store of the agent's runs.
var _ agent.MessageStore = (*session.Store)(nil)
