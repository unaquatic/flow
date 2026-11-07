package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/unaquatic/flow/app"
)

// QueuePrompt adds text to the end of the queue, to be sent once the session
// is free, and clears the input.
func (s *Service) QueuePrompt(text string) []UIEvent {
	s.state.Queue = append(s.state.Queue, text)
	return slices.Concat(s.ClearInput(), s.QueueChanged())
}

// UnqueueLast takes the prompt queued last back out of the queue and puts it
// in the input, as it was before it was queued, leaving the run in progress
// going. An emptied queue is no longer paused.
func (s *Service) UnqueueLast() []UIEvent {
	n := len(s.state.Queue)
	if n == 0 {
		return nil
	}
	text := s.state.Queue[n-1]
	s.state.Queue = slices.Delete(s.state.Queue, n-1, n)
	if len(s.state.Queue) == 0 {
		s.state.QueuePaused = false
	}
	return slices.Concat(s.QueueChanged(), s.SetInput(text))
}

// QueueChanged returns the event that shows the queue as it is.
func (s *Service) QueueChanged() []UIEvent {
	return []UIEvent{EventQueueChanged{Prompts: slices.Clone(s.state.Queue)}}
}

// SendQueued sends the first queued prompt once the active session is free:
// no run is in progress, no history move is pending, and the queue is not
// paused. A prompt that cannot be sent stays queued, the queue pauses, and
// the error shows in the indicator.
func (s *Service) SendQueued() []UIEvent {
	if len(s.state.Queue) == 0 || s.state.QueuePaused || s.state.MovesPending > 0 || s.Streaming() {
		return nil
	}
	if err := s.SendPrompt(s.state.Queue[0]); err != nil {
		s.state.QueuePaused = true
		return s.ShowError(app.ErrorOf(err))
	}
	s.state.Queue = slices.Delete(s.state.Queue, 0, 1)
	return slices.Concat(s.QueueChanged(), s.ResetError(), s.ShowIndicator(""))
}

// ResumeQueue resumes a paused queue and sends its first prompt once the
// session is free.
func (s *Service) ResumeQueue() []UIEvent {
	s.state.QueuePaused = false
	return s.SendQueued()
}

// DropQueue empties the queue, reporting in a toast how many prompts it
// dropped.
func (s *Service) DropQueue() []UIEvent {
	n := len(s.state.Queue)
	s.state.Queue, s.state.QueuePaused = nil, false
	if n == 0 {
		return nil
	}
	return slices.Concat(s.QueueChanged(), s.ShowToast(fmt.Sprintf("Dropped %s", plural(n, "queued prompt"))))
}

// BeginMove holds the queue while a history move waits for the session and
// runs.
func (s *Service) BeginMove() {
	s.state.MovesPending++
}

// EndMove releases the hold BeginMove took, and sends the next queued prompt
// once no move is pending.
func (s *Service) EndMove() []UIEvent {
	s.state.MovesPending = max(s.state.MovesPending-1, 0)
	return s.SendQueued()
}

// HandleSendNow puts text at the front of the queue, resumes the queue if it
// is paused, and stops the run in progress, so that text goes as soon as the
// session is free. An empty input does the same for the first queued prompt,
// as its box says. Answers to an open question, an empty input with nothing
// queued, and slash commands are handled as by HandleSubmit.
func (s *Service) HandleSendNow(text string) []UIEvent {
	text = strings.TrimSpace(text)
	if text == "" && s.state.Prompt == nil && len(s.state.Queue) > 0 {
		s.Stop()
		s.state.QueuePaused = false
		return s.SendQueued()
	}
	if _, command := CutCommand(text); s.state.Prompt != nil || text == "" || command {
		return s.HandleSubmit(text)
	}
	s.Stop()
	s.state.Queue = slices.Insert(s.state.Queue, 0, text)
	s.state.QueuePaused = false
	return slices.Concat(s.ClearInput(), s.QueueChanged(), s.SendQueued())
}

// plural renders a count with a regularly pluralized noun.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
