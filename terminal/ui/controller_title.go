package ui

import (
	"strings"
	"unicode"

	"github.com/unaquatic/flow/config"
)

// TitleMark starts every terminal title.
const TitleMark = "ƒ"

// Title returns the terminal title for a session titled title:
// TitleMark and " | " followed by the title, or TitleMark
// alone when the title is empty. Control characters are dropped so that the
// title cannot end the escape sequence that carries it.
func Title(title string) string {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, title)
	if title == "" {
		return TitleMark
	}
	return TitleMark + " | " + title
}

// UpdateTitle returns the event that shows the active session in the
// terminal title, as Title formats it. It returns nothing before
// session setup or when config.TerminalTitleDisabled.
func (s *Service) UpdateTitle() []UIEvent {
	if s.state.Session == nil || config.TerminalTitleDisabled() {
		return nil
	}
	// A session that is not stored yet has no title.
	var title string
	if stored, err := s.app.Sessions().Session(s.ctx, s.state.Session.ID); err == nil {
		title = stored.Title
	}
	return []UIEvent{EventTitleSet{Title: Title(title)}}
}

// SetProgress returns the event that shows or clears the progress indicator
// on the terminal tab.
func (s *Service) SetProgress(active bool) []UIEvent {
	return []UIEvent{EventProgressSet{Active: active}}
}
