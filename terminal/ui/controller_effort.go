package ui

import (
	"slices"

	"github.com/unaquatic/flow/agent"
)

// EffortChoices returns the effort levels the catalog lists for the
// terminal's model, in its order, or none.
func (s *Service) EffortChoices() []agent.ReasoningEffort {
	return s.app.Models().Efforts(s.Variant())
}

// noEffort returns the notice that the terminal's model takes no levels.
func (s *Service) noEffort() []UIEvent {
	return s.ShowKeywordToast("", s.Variant().WithEffort("").String(), " has no reasoning efforts")
}

// HandleEffort runs /effort: it shows the effort levels of the terminal's
// model, as ShowEffortMenu does, or says that the model takes none.
func (s *Service) HandleEffort() []UIEvent {
	if len(s.EffortChoices()) == 0 {
		return s.noEffort()
	}
	return s.ShowEffortMenu(s.Variant())
}

// ShowEffortMenu shows the effort levels the catalog lists for v's model, in
// its order and capitalized, such as "High", under the title effortTitle,
// searched from the input, marking the model's default with DefaultHint,
// and selecting the one v would run at for the terminal's chosen level. It is
// the page of the model menu that follows choosing model v, and what /effort
// shows for the terminal's model. Choosing a level switches the terminal to
// v at that level and saves it as the default model, as SelectModel does.
func (s *Service) ShowEffortMenu(v agent.Variant) []UIEvent {
	levels := s.app.Models().Efforts(v)
	fallback := agent.Fit(levels, "", s.app.Models().DefaultEffort(v))
	current := s.app.Models().FitEffort(v.WithEffort(s.state.ReasoningEffort)).Effort
	items := make([]MenuItem, 0, len(levels))
	for _, t := range levels {
		hint := ""
		if t == fallback {
			hint = DefaultHint
		}
		items = append(items, MenuItem{
			Name:     Capitalize(string(t)),
			Hint:     hint,
			Selected: t == current,
			Choose:   s.Closing(func() []UIEvent { return s.SelectModel(v.WithEffort(t).String()) }),
		})
	}
	return s.ShowSearchMenu(&SearchMenu{Title: effortTitle, Items: func() []MenuItem { return items }})
}

// DefaultHint follows the level a model runs at by default in the menu of
// reasoning effort levels.
const DefaultHint = "[default]"

// effortTitle heads the menu of reasoning effort levels.
const effortTitle = "Reasoning effort"

// CycleEffort moves the terminal's model to the next level it can run at,
// after the last returning to the first, as Shift+Tab does. The label under
// the input shows the level, so it adds no notice, unless the model takes no
// levels.
func (s *Service) CycleEffort() []UIEvent {
	choices := s.EffortChoices()
	if len(choices) == 0 {
		return s.noEffort()
	}
	i := slices.Index(choices, s.Variant().Effort)
	s.state.ReasoningEffort = choices[(i+1)%len(choices)]
	return s.SetVariant(s.Variant())
}
