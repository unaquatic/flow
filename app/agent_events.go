package app

import (
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

// publishAgent publishes ev, an event of an agent run of the session, as the
// app event that clients read, as AgentEvent gives it.
func (s Session) publishAgent(ev agent.Event) {
	s.logAgent(ev)
	if out := AgentEvent(s.eventBase(), ev); out != nil {
		s.publish(out)
	}
}

// logAgent logs the end of each message and tool call of a run, and each
// request it sends again.
func (s Session) logAgent(ev agent.Event) {
	switch e := ev.(type) {
	case agent.Retrying:
		s.Logger().Warn("request retrying", "message", e.MessageID, "attempt", e.Attempt, "of", e.MaxAttempts,
			"delay", e.Delay, "reason", e.Reason, "err", e.Err)
	case agent.MessageEnded:
		args := []any{"message", e.Message.ID, "model", e.Message.Model, "finish", e.Finish, "stopped", e.Stopped}
		if e.Err != nil {
			s.Logger().Warn("message ended", append(args, "err", e.Err)...)
			return
		}
		s.Logger().Info("message ended", args...)
	case agent.PartEnded:
		tp, ok := e.Part.(message.ToolPart)
		if !ok {
			return
		}
		switch st := tp.State.(type) {
		case message.ToolStateCompleted:
			s.Logger().Info("tool ended", "message", e.MessageID, "tool", tp.Name, "call", tp.CallID)
		case message.ToolStateError:
			s.Logger().Warn("tool failed", "message", e.MessageID, "tool", tp.Name, "call", tp.CallID, "err", st.Error)
		}
	}
}

// AgentEvent returns the app event that ev, an event of an agent run, is,
// stamped with base: EventMessageStarted, EventPartStarted, EventPartUpdated,
// EventPartEnded, EventMessageEnded, whose Error describes the run's error,
// or EventAgentRetrying, which retries at base's time and the delay. It
// returns nil for an event it does not know.
func AgentEvent(base EventBase, ev agent.Event) Event {
	change := func(c agent.PartChange) PartChange {
		return PartChange{EventBase: base, MessageID: c.MessageID, Part: c.Part}
	}
	switch e := ev.(type) {
	case agent.MessageStarted:
		return EventMessageStarted{EventBase: base, Message: Message{Content: e.Message}}
	case agent.PartStarted:
		return EventPartStarted{change(e.PartChange)}
	case agent.PartUpdated:
		return EventPartUpdated{change(e.PartChange)}
	case agent.PartEnded:
		return EventPartEnded{change(e.PartChange)}
	case agent.MessageEnded:
		out := EventMessageEnded{EventBase: base, Message: Message{Content: e.Message}, Finish: e.Finish, Stopped: e.Stopped}
		if e.Err != nil {
			u := ErrorOf(e.Err)
			out.Error = &u
		}
		return out
	case agent.Retrying:
		out := EventAgentRetrying{EventBase: base, MessageID: e.MessageID, Attempt: e.Attempt, MaxAttempts: e.MaxAttempts,
			RetryAt: base.Timestamp.Add(e.Delay), Reason: e.Reason, Error: NewError(CodeUnknown, e.Reason)}
		if e.Err != nil {
			out.Error = ErrorOf(e.Err)
		}
		return out
	}
	return nil
}
