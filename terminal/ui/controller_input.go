package ui

import (
	"slices"
	"strings"
	"time"
)

// HandleInputChange takes the input's text as it changes. While a banner or
// the session list is open the text is their search; otherwise it opens or
// closes the slash menu.
func (s *Service) HandleInputChange(text string) []UIEvent {
	if s.state.SessionMenuVisible || s.state.Banner != NoBanner {
		return s.FilterMenu(text)
	}
	return s.UpdateSlashMenu(text)
}

// FilterMenu narrows the open session list or banner menu to query. It
// returns nothing when neither is open or the banner has no menu.
func (s *Service) FilterMenu(query string) []UIEvent {
	if s.state.SessionMenuVisible {
		return s.FilterSessions(query)
	}
	return s.HandleBannerSearch(query)
}

// QuitWindow is how long after an idle Ctrl+C a second press quits. The
// empty input shows QuitHint meanwhile.
const QuitWindow = time.Second

// QuitHint is what the empty input shows while a second Ctrl+C quits.
const QuitHint = "[ctrl+c quit]"

// HandleCtrlC stops an active run or clears the input. When idle, it arms the
// quit, and a second press within QuitWindow quits. While a banner or the
// session list is open, it closes it instead, as CloseMenu does, and leaves a
// run going. It always reports that the key was consumed.
func (s *Service) HandleCtrlC(hasInput bool) (bool, []UIEvent) {
	if s.state.Prompt != nil {
		return true, s.CancelPrompt()
	}
	if out := s.CloseMenu(); out != nil {
		return true, out
	}
	now := time.Now()
	busy := s.Streaming()
	hadWork := busy || hasInput

	if !hadWork && s.QuitArmed(now) {
		return true, []UIEvent{EventQuit{}}
	}
	if busy {
		s.Stop()
	}
	if hadWork {
		return true, slices.Concat(s.ClearInput(), s.SetStatus(StatusHintStyled()), s.HideIndicator())
	}

	// Remember the first idle press, and show the key that quits until the
	// second press no longer does.
	s.state.LastCtrlCTime = now
	return true, slices.Concat(s.ClearInput(), []UIEvent{EventQuitArmed{}})
}

// QuitArmed reports whether an idle Ctrl+C at now quits: whether the press
// before it armed the quit less than QuitWindow earlier.
func (s *Service) QuitArmed(now time.Time) bool {
	armed := s.state.LastCtrlCTime
	return !armed.IsZero() && now.Sub(armed) < QuitWindow
}

// CloseMenu closes the open banner, such as /settings, /model, /login, or
// the session tree, or else the session list that /resume opens, as Esc
// does, and returns the events that close it, or nil when neither is open.
func (s *Service) CloseMenu() []UIEvent {
	switch {
	case s.state.Banner != NoBanner:
		return s.CancelBanner()
	case s.state.SessionMenuVisible:
		return s.HideSessionMenu()
	}
	return nil
}

// HandleEscape cancels an open question, or stops an active run and resets its
// status. It reports whether the key was consumed.
func (s *Service) HandleEscape() (bool, []UIEvent) {
	if s.state.Prompt != nil {
		return true, s.CancelPrompt()
	}
	if !s.Streaming() {
		return false, nil
	}
	s.Stop()
	return true, slices.Concat(s.SetStatus(StatusHintStyled()), s.HideIndicator())
}

// HandleCtrlO shows the details of every tool call, and of the changes of
// every step, when they are hidden and hides them when they are shown, and
// reports that it did. While the session tree is open, Ctrl+O cycles its
// filter instead, so the key is left to it.
func (s *Service) HandleCtrlO() (bool, []UIEvent) {
	if s.state.Banner == TreeBanner {
		return false, nil
	}
	return true, s.SetToolsExpanded(!s.state.ToolsExpanded)
}

// HandleCtrlU pins the Usage box above the input box, so that it shows
// while the slash menu is closed too, or unpins it once pinned.
func (s *Service) HandleCtrlU() []UIEvent {
	s.state.UsagePinned = !s.state.UsagePinned
	return []UIEvent{EventUsagePinnedSet{Pinned: s.state.UsagePinned}}
}

// SetToolsExpanded shows or hides the details of every tool call and of the
// changes of every step. Those added later follow it. During a run, the
// indicator then names what Ctrl+O does next.
func (s *Service) SetToolsExpanded(expanded bool) []UIEvent {
	s.state.ToolsExpanded = expanded
	out := []UIEvent{EventToolsExpandedSet{Expanded: expanded}}
	if s.state.Working {
		out = append(out, WorkingIndicator(s.TurnElapsed(), expanded))
	}
	return out
}

// HandleSubmit answers an open question, runs a recognized slash command,
// typed by its name or an alias, or adds text to the queue, which sends it
// as soon as the session is free. During a run, /demo stops the run;
// commands that replace the session or move its history stop the run
// themselves. Enter on an empty input resumes a paused queue.
func (s *Service) HandleSubmit(text string) []UIEvent {
	if s.state.Prompt != nil {
		return s.AnswerPrompt(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		if s.state.QueuePaused {
			return s.ResumeQueue()
		}
		return nil
	}

	if name, ok := CutCommand(text); ok {
		if cmd, ok := LookupCommand(s.CommandList(), Command(name)); ok {
			return slices.Concat(s.ClearInput(), cmd.Run(s))
		}
	}
	return slices.Concat(s.QueuePrompt(text), s.SendQueued())
}

// Quit asks the UI to exit.
func (s *Service) Quit() []UIEvent {
	return []UIEvent{EventQuit{}}
}
