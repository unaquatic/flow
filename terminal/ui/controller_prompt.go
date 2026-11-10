package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/fluid/style"
)

// TextPrompt makes the next submitted input the answer to a question instead
// of a message to the agent. The input shows the answer as typed.
type TextPrompt struct {
	// Hint is the status-line hint while the question is open.
	Hint style.StyledText
	// Placeholder shows in the input while it is empty and the question is
	// open, such as "[provider]/[model]", or is "".
	Placeholder string
	// Submit receives the answer, trimmed of surrounding white space. It can
	// be empty.
	Submit func(answer string) []UIEvent
}

// AskText shows question and makes the next submitted input its answer, which
// submit receives. Escape or Ctrl+C cancels the question.
func (s *Service) AskText(question string, hint style.StyledText, submit func(answer string) []UIEvent) []UIEvent {
	return s.Ask(question, &TextPrompt{Hint: hint, Submit: submit})
}

// Ask opens p, emptying the input, which shows p's placeholder, and shows
// question in the transcript, unless question is empty. The next submitted
// input is p's answer. Escape or Ctrl+C cancels it.
func (s *Service) Ask(question string, p *TextPrompt) []UIEvent {
	s.state.Prompt = p
	events := slices.Concat(s.ClearInput(), s.SetStatus(p.Hint), []UIEvent{EventInputHintChanged{}})
	if question != "" {
		events = append(events, s.ShowToast(question)...)
	}
	return events
}

// AnswerPrompt closes the open question and hands text to it.
func (s *Service) AnswerPrompt(text string) []UIEvent {
	p := s.state.Prompt
	s.state.Prompt = nil
	return slices.Concat(s.ClearInput(), s.SetStatus(StatusHintStyled()), []UIEvent{EventInputHintChanged{}}, p.Submit(strings.TrimSpace(text)))
}

// CancelPrompt closes the open question without an answer.
func (s *Service) CancelPrompt() []UIEvent {
	s.state.Prompt = nil
	return slices.Concat(s.ClearInput(), s.SetStatus(StatusHintStyled()), []UIEvent{EventInputHintChanged{}}, s.ShowToast("Cancelled."))
}

// InputHint returns the hint the input shows while it is empty at now:
// QuitHint while a second Ctrl+C quits, and otherwise the placeholder of the
// open question, or "".
func (s *Service) InputHint(now time.Time) string {
	if s.state.Login != nil {
		return "Paste code#state or callback URL"
	}
	if s.QuitArmed(now) {
		return QuitHint
	}
	if p := s.state.Prompt; p != nil {
		return p.Placeholder
	}
	return ""
}

// PromptHintStyled returns the status-line hint of a question whose answer
// enter submits for action.
func PromptHintStyled(action string) style.StyledText {
	key := style.New().WithFg(textFg).Func()
	dim := style.New().WithFg(mutedFg).Func()
	return style.NewStyledText(key("enter"), dim(" "+action+" "), key("esc"), dim(" cancel"))
}
