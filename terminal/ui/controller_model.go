package ui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/textbuf"
)

// SetModelLabel replaces the model the slash menu's status row names.
func (s *Service) SetModelLabel(label string) []UIEvent {
	s.state.ModelLabel = label
	return []UIEvent{EventModelLabelChanged{Label: label}}
}

// SessionUsage returns the token use of the transcript, priced from the
// catalog as it is now, each reply at the price of the variant its model
// ran as, with the prompt window of the terminal's model.
func (s *Service) SessionUsage() SessionUsage {
	cat := s.app.Models().Catalog()
	u := SumUsage(s.state.Messages, func(msg ChatMessage) (gateway.Price, bool) {
		v := msg.Variant(cat)
		r, ok := cat.Resolve(v.Provider, v.ID())
		if !ok || r.Price == nil {
			return gateway.Price{}, false
		}
		return *r.Price, true
	})
	if off, ok := agent.Lookup(cat, s.Variant()); ok {
		u.Window = off.Limit.PromptWindow()
	}
	return u
}

// ContextWindow returns the most tokens a prompt of the model that produced
// msg holds, as the catalog lists it, or zero when it does not list it.
func (s *Service) ContextWindow(msg ChatMessage) int {
	cat := s.app.Models().Catalog()
	off, ok := agent.Lookup(cat, msg.Variant(cat))
	if !ok {
		return 0
	}
	return off.Limit.PromptWindow()
}

// AssignWindows records on each reply in msgs that has usage the window of
// its model, which its footer shows its context as a share of.
func (s *Service) AssignWindows(msgs []ChatMessage) {
	for i := range msgs {
		if m := &msgs[i]; m.Role == message.RoleAgent && !m.Usage.IsZero() {
			m.Window = s.ContextWindow(*m)
		}
	}
}

// Variant returns the model variant this terminal sends its prompts to.
func (s *Service) Variant() agent.Variant {
	return s.app.Models().ParseVariant(s.state.Model)
}

// UpdateUsage recounts the token use that the slash menu's status row
// shows.
func (s *Service) UpdateUsage() []UIEvent {
	s.state.Usage = s.SessionUsage()
	return []UIEvent{EventUsageChanged{Usage: s.state.Usage}}
}

// modelsTitle heads the model menu, and the banner that shows it loading.
const modelsTitle = "Models"

// ShowModelMenu requests the model list and shows that it is loading.
func (s *Service) ShowModelMenu() []UIEvent {
	s.OpenBanner(ModelBanner)
	s.app.Models().StartListings(s.ctx)
	return []UIEvent{EventBannerShow{Title: modelsTitle, Text: "Loading models..."}}
}

// VariantName returns the catalog's name of v, a variant of off, such as
// "Model A Fast", or v's model name when the catalog gives it none.
func VariantName(off gateway.Offering, v agent.Variant) string {
	name := off.Name
	if v.Option != "" {
		name = off.Variants[v.Option].Name
	}
	if name == "" {
		return v.ID()
	}
	return name
}

// DisplayName returns the name of v that the session info and the Usage box
// show: the catalog's name of its model's lab, its name as VariantName gives
// it, and the catalog's name of its reasoning effort, such as "Lab A Model A
// Fast High". The lab is left out when the name starts with it already. A
// variant the catalog does not list reads as its lab, by the catalog's name
// or as Unknown writes its ID, and then its model as Unknown writes its ID,
// such as "Lab A Unknown [model-h]" and "Unknown [lab-z] Unknown [model-z]",
// or as Unknown of its ID alone when it names no lab, such as "Unknown
// [model-x]", and then the name of the effort its ID ends in.
func DisplayName(cat *gateway.Catalog, v agent.Variant) string {
	var words []string
	effort := v.Effort
	if off, ok := agent.Lookup(cat, v); ok {
		name := VariantName(off, v)
		lab := cat.Labs[cmp.Or(off.Lab, gateway.ModelID(v.Model).Lab())].Name
		if lab != "" && !strings.HasPrefix(name, lab+" ") {
			words = append(words, lab)
		}
		words = append(words, name)
	} else {
		id := v.ID()
		if effort == "" {
			id, effort = SplitEffort(id)
		}
		if lab, model, ok := strings.Cut(id, "/"); ok {
			words = append(words, cmp.Or(cat.Labs[gateway.ProviderID(lab)].Name, Unknown(lab)))
			id = model
		}
		words = append(words, Unknown(id))
	}
	if effort != "" {
		words = append(words, cmp.Or(cat.Efforts[gateway.Effort(effort)].Name, string(effort)))
	}
	return strings.Join(words, " ")
}

// ModelDisplayName returns model, a variant in
// "provider/model:option:effort" form, as DisplayName names it from the
// catalog, or "" when model is empty.
func (s *Service) ModelDisplayName(model string) string {
	if model == "" {
		return ""
	}
	return DisplayName(s.app.Models().Catalog(), s.app.Models().ParseVariant(model))
}

// modelColumnGap is the space between the model menu's columns: its
// variants' names, their IDs, and their providers.
const modelColumnGap = 3

// ModelsListed replaces the loading banner with the listed models, searched
// from the input, selecting the active one, or reports that none were
// returned. Each row reads in three aligned columns: the variant's name, and
// then, dim, its ID and its provider, such as
// "Model A Fast   lab-a/model-a:fast   [provider-a]". The search finds a
// row by its name, its ID, or its whole "provider/model" form. It does
// nothing once the loading banner has closed. Choosing a model that takes
// reasoning efforts shows them next, as ShowEffortMenu does; choosing one
// that takes none switches to it and saves it as the default model, as
// SelectModel does.
func (s *Service) ModelsListed(listings []app.ModelListing) []UIEvent {
	if s.state.Banner != ModelBanner {
		return nil
	}

	type row struct {
		off gateway.Offering
		v   agent.Variant
	}
	// The columns fit every row, not only those a search shows, so that
	// they stay put while the search narrows the menu.
	var rows []row
	nameWidth, idWidth := 0, 0
	for _, listing := range listings {
		for _, m := range listing.Models {
			for _, v := range app.ModelVariants(listing.Provider, m) {
				rows = append(rows, row{m.Offering, v})
				nameWidth = max(nameWidth, textbuf.StringWidth(VariantName(m.Offering, v)))
				idWidth = max(idWidth, textbuf.StringWidth(v.ID()))
			}
		}
	}

	// Rows name models without levels, which the next page sets.
	current := s.Variant().WithEffort("")
	items := make([]MenuItem, 0, len(rows))
	for _, r := range rows {
		v := r.v
		choose := s.Closing(func() []UIEvent { return s.SelectModel(v.String()) })
		if len(s.app.Models().Efforts(v)) > 0 {
			choose = func() []UIEvent { return s.ShowEffortMenu(v) }
		}
		id := v.ID()
		pad := strings.Repeat(" ", idWidth-textbuf.StringWidth(id)+modelColumnGap)
		items = append(items, MenuItem{
			Name:     VariantName(r.off, v),
			Desc:     id + pad + Tag(string(v.Provider)),
			Aliases:  []string{id, v.String()},
			Selected: v == current,
			Choose:   choose,
		})
	}
	if len(items) == 0 {
		return slices.Concat(s.CancelBanner(), s.ShowToast("No models available. Run /login first."))
	}
	return s.ShowSearchMenu(&SearchMenu{
		Title:     modelsTitle,
		Items:     func() []MenuItem { return items },
		NameWidth: nameWidth + modelColumnGap,
		Custom:    s.AskCustomModel,
	})
}

// CustomModelPlaceholder shows in the empty input while it asks for a model
// typed in, as AskCustomModel does.
const CustomModelPlaceholder = "[provider]/[model]"

// AskCustomModel closes the model menu and asks for a model typed in, which
// the menu need not list, in "provider/model" form, as SelectModel takes it,
// with CustomModelPlaceholder in the empty input. The input starts with
// query, the menu's search, so that a model searched for and not found need
// not be typed again. Enter switches to the model and saves it as the
// default model, as SelectModel does; an empty answer leaves the model as
// it is.
func (s *Service) AskCustomModel(query string) []UIEvent {
	events := slices.Concat(s.CancelBanner(), s.Ask("", &TextPrompt{
		Hint:        PromptHintStyled("use model"),
		Placeholder: CustomModelPlaceholder,
		Submit: func(model string) []UIEvent {
			if model == "" {
				return nil
			}
			return s.SelectModel(model)
		},
	}))
	if query = strings.TrimSpace(query); query != "" {
		events = append(events, EventInputSet{Text: query})
	}
	return events
}

// ModelsListFailed closes the loading banner and reports why the models
// could not be listed. It does nothing once the loading banner has closed.
func (s *Service) ModelsListFailed(reason string) []UIEvent {
	if s.state.Banner != ModelBanner {
		return nil
	}
	return slices.Concat(s.CancelBanner(), s.ShowToast("Listing models failed: "+reason))
}

// SelectModel makes model, a variant as Models.ParseVariant reads it, the
// model this terminal sends its prompts to, and saves it as the default
// model in the config file, which a request that names no model goes to and
// a new terminal starts with, so that the model chosen is the one flow keeps
// using. It is also the terminal's DefaultModel, which its new sessions
// start with. A level model names becomes the terminal's chosen level; a
// model that names none runs at the chosen level, as SetVariant fits it, and
// is saved at that level. A notice says what it switched to, or, when the
// config file cannot be written, that it switched but could not save.
func (s *Service) SelectModel(model string) []UIEvent {
	v := s.app.Models().ParseVariant(model)
	if v.Effort != "" {
		s.state.ReasoningEffort = v.Effort
	}
	events := s.SetVariant(v)
	s.state.DefaultModel = s.state.Model
	if err := s.app.Models().SaveDefault(s.state.Model); err != nil {
		return slices.Concat(events,
			s.ShowKeywordToast("Switched to ", s.state.Model, ""),
			s.ShowToast("Saving the default model failed: "+err.Error()))
	}
	return slices.Concat(events, s.ShowKeywordToast("Switched to ", s.state.Model, " as the default model"))
}

// SetVariant makes v, at the terminal's chosen reasoning effort fitted to its
// model as Models.FitEffort fits it, the model this terminal sends its
// prompts to, and updates the label and usage that depend on it.
func (s *Service) SetVariant(v agent.Variant) []UIEvent {
	s.state.Model = s.app.Models().FitEffort(v.WithEffort(s.state.ReasoningEffort)).String()
	return slices.Concat(s.SetModelLabel(s.state.Model), s.UpdateUsage())
}

// RestoreSessionModel makes the model variant the active session's
// transcript last ran, as its newest message that names a model records it,
// the terminal's model, with its level as the chosen one, or, for a
// transcript that names no model, the terminal's DefaultModel, as
// RestoreDefaultModel does. It leaves DefaultModel, and the config file's
// default model, as they are.
func (s *Service) RestoreSessionModel() []UIEvent {
	for _, m := range slices.Backward(s.state.Messages) {
		if m.Model == "" {
			continue
		}
		v := m.Variant(s.app.Models().Catalog())
		s.state.ReasoningEffort = v.Effort
		return s.SetVariant(v)
	}
	return s.RestoreDefaultModel()
}

// RestoreDefaultModel makes the terminal's DefaultModel its model, with its
// level as the chosen one, as a new session starts. It does nothing while
// DefaultModel is empty.
func (s *Service) RestoreDefaultModel() []UIEvent {
	if s.state.DefaultModel == "" {
		return nil
	}
	v := s.app.Models().ParseVariant(s.state.DefaultModel)
	s.state.ReasoningEffort = v.Effort
	return s.SetVariant(v)
}
