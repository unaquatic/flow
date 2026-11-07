package ui

// ToggleInlineMode switches between inline rendering and the alternate screen,
// and saves the mode, as PersistTerminal does, for the terminal to start in.
// The transcript moves between containers unchanged, so the event carries no
// messages, and the switch adds no notice to it.
func (s *Service) ToggleInlineMode() []UIEvent {
	s.state.Inline = !s.state.Inline
	return append([]UIEvent{EventInlineModeSet{Inline: s.state.Inline}}, s.PersistTerminal()...)
}
