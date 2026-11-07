package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/demo"
)

// queueTest drives a Service with a fresh session. Its app has no account,
// so every real send fails once it starts; the demo runs until stopped.
type queueTest struct {
	t      *testing.T
	s      *Service
	a      *app.App
	events <-chan app.Event
}

func newQueueTest(t *testing.T) *queueTest {
	t.Helper()
	s, a := newTestService(t)
	events := a.Subscribe(t.Context())
	s.NewSession()
	return &queueTest{t: t, s: s, a: a, events: events}
}

// until applies app events to the service, as the UI does, up to and
// including the next event of type want, and returns that event.
func (q *queueTest) until(want app.EventType) app.Event {
	q.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-q.events:
			q.s.ApplyAgentEvent(ev)
			if ev.EventType() == want {
				return ev
			}
		case <-deadline:
			q.t.Fatalf("timed out waiting for %s", want)
		}
	}
}

// startDemo starts the demo and applies events until it has started.
func (q *queueTest) startDemo() {
	q.t.Helper()
	q.s.StartScript("Demo", demo.Script())
	if !q.s.Streaming() {
		q.t.Fatal("the demo did not start")
	}
	q.until(app.TypeAgentStarted)
}

// wantQueue checks the queued prompts.
func (q *queueTest) wantQueue(want ...string) {
	q.t.Helper()
	if !slices.Equal(q.s.state.Queue, want) {
		q.t.Errorf("queue = %q, want %q", q.s.state.Queue, want)
	}
}

// TestQueue_SendsWhenTheRunEnds checks that a prompt submitted during a run
// waits in the queue, and is sent once the run has ended.
func TestQueue_SendsWhenTheRunEnds(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.HandleSubmit("next")
	q.wantQueue("next")

	q.s.Stop()
	q.until(app.TypeAgentEnded)
	q.wantQueue()
	if !q.s.Streaming() {
		t.Fatal("the queued prompt did not start")
	}
	if end := q.until(app.TypeAgentEnded).(app.EventAgentEnded); end.Error == nil {
		t.Fatal("the queued prompt ended without the missing account's error")
	}
	if q.s.state.QueuePaused {
		t.Error("an empty queue paused after a failed run")
	}
}

// TestDemoShowsTheRun checks that the demo shows its run as any other run
// does: the indicator shows the time it has taken, which brings the run's
// keys into the footer, until it ends.
func TestDemoShowsTheRun(t *testing.T) {
	q := newQueueTest(t)
	out := q.s.StartScript("Demo", demo.Script())
	shown := slices.ContainsFunc(out, func(ev UIEvent) bool {
		e, ok := ev.(EventIndicatorShow)
		return ok && strings.HasPrefix(e.Text, "Working [")
	})
	if !shown || !q.s.state.Working {
		t.Fatalf("starting the demo: indicator shown %v, working %v; want the run's time shown", shown, q.s.state.Working)
	}
	q.until(app.TypeAgentStarted)
	q.s.Stop()
	q.until(app.TypeAgentEnded)
	if q.s.state.Working {
		t.Error("the indicator still shows the run's time after the demo ended")
	}
}

// TestQueue_PausesAfterAFailedRunUntilResumed checks that the queue holds its
// prompts after a run fails, and that Enter on an empty input resumes it.
func TestQueue_PausesAfterAFailedRunUntilResumed(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.HandleSubmit("a")
	q.s.HandleSubmit("b")

	q.s.Stop()
	q.until(app.TypeAgentEnded)
	q.wantQueue("b")
	q.until(app.TypeAgentEnded)
	if !q.s.state.QueuePaused || q.s.Streaming() {
		t.Fatalf("after a failed run: paused = %v, running = %v; want paused and idle", q.s.state.QueuePaused, q.s.Streaming())
	}
	q.wantQueue("b")

	q.s.HandleSubmit("")
	q.wantQueue()
	if q.s.state.QueuePaused || !q.s.Streaming() {
		t.Fatalf("after resuming: paused = %v, running = %v; want the next prompt running", q.s.state.QueuePaused, q.s.Streaming())
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_SendNowGoesFirst checks that Alt+Enter stops the run and sends
// its prompt before those already queued.
func TestQueue_SendNowGoesFirst(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.HandleSubmit("later")
	q.s.HandleSendNow("now")
	q.wantQueue("now", "later")

	q.until(app.TypeAgentEnded)
	q.wantQueue("later")
	if !q.s.Streaming() {
		t.Fatal("the prompt sent now did not start")
	}
	q.until(app.TypeAgentEnded)
	q.wantQueue("later")
}

// TestQueue_SendNowSendsTheFirstQueued checks that Alt+Enter on an empty
// input stops the run and sends the first queued prompt as soon as the
// session is free, as its box says, ahead of the prompts behind it.
func TestQueue_SendNowSendsTheFirstQueued(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.HandleSubmit("first")
	q.s.HandleSubmit("second")
	q.s.HandleSendNow("")

	q.until(app.TypeAgentEnded)
	q.wantQueue("second")
	if !q.s.Streaming() {
		t.Fatal("the first queued prompt did not start")
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_UndoTakesBackTheLastQueued checks that /undo takes the prompts
// queued back out of the queue, the last first, into the input, leaving the
// run going, and only then moves the workspace back a turn.
func TestQueue_UndoTakesBackTheLastQueued(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.HandleSubmit("first")
	q.s.HandleSubmit("second")

	for _, tt := range []struct {
		input string
		queue []string
	}{
		{input: "second", queue: []string{"first"}},
		{input: "first", queue: nil},
	} {
		out := q.s.HandleSubmit("/undo")
		q.wantQueue(tt.queue...)
		if !slices.Contains(out, UIEvent(EventInputSet{Text: tt.input})) {
			t.Errorf("/undo = %v, want %q back in the input", out, tt.input)
		}
		if slices.Contains(out, UIEvent(EventUndoRequested{})) || !q.s.Streaming() {
			t.Errorf("/undo of %q moved the workspace back or stopped the run", tt.input)
		}
	}

	if out := q.s.HandleSubmit("/undo"); !slices.Contains(out, UIEvent(EventUndoRequested{})) {
		t.Errorf("/undo with nothing queued = %v, want a move back a turn", out)
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_SendNowResumesAPausedQueue checks that Alt+Enter sends its prompt
// at once from a paused queue, and resumes the prompts behind it.
func TestQueue_SendNowResumesAPausedQueue(t *testing.T) {
	q := newQueueTest(t)
	q.s.state.Queue, q.s.state.QueuePaused = []string{"waiting"}, true

	q.s.HandleSendNow("now")
	q.wantQueue("waiting")
	if q.s.state.QueuePaused || !q.s.Streaming() {
		t.Fatalf("paused = %v, running = %v; want the prompt sent and the queue resumed", q.s.state.QueuePaused, q.s.Streaming())
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_PromptThatCannotStartStaysQueued checks that a prompt the session
// refuses stays in the queue, which pauses with the refusal shown, until
// Enter on an empty input sends it.
func TestQueue_PromptThatCannotStartStaysQueued(t *testing.T) {
	q := newQueueTest(t)
	id := q.s.SessionID()
	op, _, err := q.a.Claim(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	out := q.s.HandleSubmit("refused")
	q.wantQueue("refused")
	if !q.s.state.QueuePaused || q.s.Streaming() {
		t.Fatalf("paused = %v, running = %v; want the prompt held in a paused queue", q.s.state.QueuePaused, q.s.Streaming())
	}
	refusal := func(e UIEvent) bool {
		ind, ok := e.(EventIndicatorShow)
		return ok && ind.Text == app.ErrBusy.Error()
	}
	if !slices.ContainsFunc(out, refusal) {
		t.Errorf("events = %+v, want the refusal shown", out)
	}

	q.a.Release(id, op)
	q.s.HandleSubmit("")
	q.wantQueue()
	if !q.s.Streaming() {
		t.Fatal("the held prompt did not start once resumed")
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_EnterBeforeTheRunsEndIsQueued checks that a prompt submitted
// after the app has freed the session, but before the run's end reached the
// terminal, waits for that end instead of starting a run whose events would
// mix with the old one's.
func TestQueue_EnterBeforeTheRunsEndIsQueued(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.Stop()
	if err := q.a.Session(q.s.SessionID()).Wait(); err != nil {
		t.Fatal(err)
	}

	q.s.HandleSubmit("next")
	q.wantQueue("next")
	q.until(app.TypeAgentEnded)
	q.wantQueue()
	if !q.s.Streaming() {
		t.Fatal("the queued prompt did not start after the run's end")
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_HeldDuringAMove checks that a prompt submitted while a history
// move is pending waits for the move, and is sent once it ends.
func TestQueue_HeldDuringAMove(t *testing.T) {
	q := newQueueTest(t)
	q.s.BeginMove()
	q.s.HandleSubmit("after")
	q.wantQueue("after")
	if q.s.Streaming() {
		t.Fatal("the prompt started during the move")
	}

	q.s.EndMove()
	q.wantQueue()
	if !q.s.Streaming() {
		t.Fatal("the prompt did not start after the move")
	}
	q.until(app.TypeAgentEnded)
}

// TestQueue_DroppedOnNewSession checks that starting a new session drops the
// queued prompts and says how many.
func TestQueue_DroppedOnNewSession(t *testing.T) {
	q := newQueueTest(t)
	q.startDemo()
	q.s.HandleSubmit("lost")

	out := q.s.ClearMessages()
	q.wantQueue()
	if q.s.Streaming() {
		t.Error("the new session is marked running")
	}
	dropped := func(e UIEvent) bool {
		toast, ok := toastOf(e)
		return ok && toast.Text == "Dropped 1 queued prompt"
	}
	if !slices.ContainsFunc(out, dropped) {
		t.Errorf("events = %+v, want the dropped prompt reported", out)
	}
}
