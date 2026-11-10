package ui

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// AddMessage adds a message to UI state and returns its display event. It does
// not persist the message. Consecutive messages with the same role share a
// parent bubble, and each message holds the parts of the one before it, the
// notices between them aside.
func (s *Service) AddMessage(msg ChatMessage) []UIEvent {
	if prev, i := s.LastMessage(); i >= 0 {
		if prev.Role == msg.Role {
			msg.ParentID = prev.ParentID
		}
		msg.Prev = slices.Clone(prev.Parts)
	}
	if msg.ParentID == "" {
		msg.ParentID = msg.ID
	}
	s.state.Messages = append(s.state.Messages, msg)
	return []UIEvent{EventMessageAdded{Msg: msg}}
}

// LastMessage returns the last message of the transcript, past the notices
// after it, and its index in it, or nil and -1 when it holds none.
func (s *Service) LastMessage() (*ChatMessage, int) {
	for i, m := range slices.Backward(s.state.Messages) {
		if m.Notice == nil {
			return &s.state.Messages[i], i
		}
	}
	return nil, -1
}

// MessageCount returns the number of messages of the transcript, its notices
// left out.
func (s *Service) MessageCount() int {
	n := 0
	for _, m := range s.state.Messages {
		if m.Notice == nil {
			n++
		}
	}
	return n
}

// ApplyPart puts part into message id of the transcript, replacing the part
// with the same ID or adding it at the end, records in the message's Live
// whether the part is live, still to take its final form, and returns the
// message's display update. Token usage shows once the message ends, after
// its tools.
func (s *Service) ApplyPart(id message.ID, part message.ContentPart, live bool) []UIEvent {
	i := slices.IndexFunc(s.state.Messages, func(m ChatMessage) bool { return m.ID == id })
	if i < 0 {
		return nil
	}
	m := &s.state.Messages[i]
	if j := slices.IndexFunc(m.Parts, func(p message.ContentPart) bool { return p.PartID() == part.PartID() }); j >= 0 {
		m.Parts[j] = part
	} else {
		m.Parts = append(m.Parts, part)
	}
	if pid := part.PartID(); pid != "" {
		m.Live = slices.DeleteFunc(m.Live, func(p message.PartID) bool { return p == pid })
		if live {
			m.Live = append(m.Live, pid)
		}
	}
	msg := *m
	msg.Parts = slices.Clone(m.Parts)
	msg.Live = slices.Clone(m.Live)
	msg.Usage = message.Usage{}
	return []UIEvent{EventMessageUpdated{Msg: msg}}
}

// EndMessage records the usage, model variant and creation time that msg
// ended with in its transcript entry, with the window of its model when it
// has usage, and that none of its parts is live any longer, and returns the
// entry. It reports whether msg ended with usage, which changes the footer's
// totals, and returns false for a message that is not in the transcript.
func (s *Service) EndMessage(msg message.Content) (ChatMessage, bool) {
	i := slices.IndexFunc(s.state.Messages, func(m ChatMessage) bool { return m.ID == msg.ID })
	if i < 0 {
		return ChatMessage{}, false
	}
	m := &s.state.Messages[i]
	m.Live = nil
	m.Usage = msg.Usage()
	if msg.Model != "" {
		m.Provider, m.Model = msg.Provider, msg.Model
	}
	if !msg.CreatedAt.IsZero() {
		m.CreatedAt = msg.CreatedAt
	}
	usage := !m.Usage.IsZero()
	if usage {
		m.Window = s.ContextWindow(*m)
	}
	return *m, usage
}

// StartTurn opens a turn of the active session at start, whose messages are
// those added from now on.
func (s *Service) StartTurn(start time.Time) {
	s.state.TurnStart, s.state.TurnFrom = start, len(s.state.Messages)
}

// EndTurn closes the turn in progress at end. When the turn ends with a reply
// it added, it records how long the turn took on that reply and returns the
// event that shows it there. It does nothing while no turn is in progress.
func (s *Service) EndTurn(end time.Time) []UIEvent {
	start := s.state.TurnStart
	s.state.TurnStart = time.Time{}
	last, i := s.LastMessage()
	if start.IsZero() || i < s.state.TurnFrom {
		return nil
	}
	if last.Role != message.RoleAgent {
		return nil
	}
	last.Worked = end.Sub(start)
	return []UIEvent{EventTurnEnded{MessageID: last.ID, Worked: last.Worked}}
}

// ApplyAgentEvent updates conversation state and returns events for the UI.
// It drops events of sessions other than the active one, such as those a
// replaced session's run publishes while it stops. The caller must call it
// from the event-loop goroutine.
func (s *Service) ApplyAgentEvent(ev app.Event) []UIEvent {
	if id := ev.Base().SessionID; id != "" && (s.state.Session == nil || id != s.state.Session.ID) {
		return nil
	}

	// Update conversation state before forwarding streaming content to the UI.
	var out []UIEvent
	switch e := ev.(type) {
	case app.EventMessageStarted:
		// A prompt opens a turn, and ends the turn before it in the same
		// run, as a demo's prompts do.
		if e.Message.Role == message.RoleUser {
			out = s.EndTurn(e.Timestamp)
			s.StartTurn(e.Timestamp)
		}
		out = append(out, s.AddMessage(ChatMessageOf(e.Message.Content))...)

	case app.EventPartStarted:
		// A usage part is final as it starts, though it ends in an event of
		// its own, as the agent records it.
		_, final := e.Part.(message.UsagePart)
		out = s.ApplyPart(e.MessageID, e.Part, !final)

	case app.EventPartUpdated:
		out = s.ApplyPart(e.MessageID, e.Part, true)

	case app.EventPartEnded:
		out = s.ApplyPart(e.MessageID, e.Part, false)

	case app.EventMessageEnded:
		// A failed message shows its error through the run's end.
		update := ChatMessageOf(e.Message.Content)
		ended, usage := s.EndMessage(e.Message.Content)
		update.Window = ended.Window
		out = []UIEvent{EventMessageUpdated{Msg: update}}
		if usage {
			out = append(out, s.UpdateUsage()...)
		}

	case app.EventAgentStarted:
		s.StartTurn(e.Timestamp)
		out = s.SetProgress(true)

	case app.EventAgentEnded:
		s.state.Running = false
		out = slices.Concat(s.EndTurn(e.Timestamp), s.SetStatus(StatusHintStyled()), s.SetProgress(false))
		if e.Error != nil {
			out = append(out, s.ShowError(*e.Error)...)
			// Queued prompts wait rather than meet the same failure.
			if len(s.state.Queue) > 0 {
				s.state.QueuePaused = true
			}
		} else {
			out = append(out, s.HideIndicator()...)
		}
		out = append(out, s.SendQueued()...)

	case app.EventSessionLoaded:
		out = slices.Concat(s.LoadMessages(e.Messages), s.UpdateUsage())

	case app.EventSessionUpdated:
		// Such as when an extension titles the session.
		out = s.ShowTitle(e.Title)

	case app.EventLoginPrompt:
		if e.State != nil {
			out = s.AskLoginCode(e)
		} else {
			out = []UIEvent{EventBannerUpdate{Text: e.Prompt().String()}}
		}

	case app.EventLoginSucceeded:
		out = s.SettleLogin(fmt.Sprintf("logged in to %s account %s.", s.ProviderName(e.Provider), e.Account))

	case app.EventLoginFailed:
		if p := s.state.Login; p != nil && p.Provider == e.Provider && p.Account == e.Account && e.Retry {
			s.state.LoginCompleting = false
			out = []UIEvent{s.LoginPanel(e.Error.Message)}
			break
		}
		if e.Error.Message == context.Canceled.Error() {
			out = s.SettleLogin(fmt.Sprintf("login to %s account %s cancelled.", s.ProviderName(e.Provider), e.Account))
		} else {
			out = s.FailLogin(e)
		}

	case app.EventModelsListed:
		if e.Error != nil {
			out = s.ModelsListFailed(e.Error.Message)
		} else {
			out = s.ModelsListed(e.Listings)
		}
	}
	return out
}
