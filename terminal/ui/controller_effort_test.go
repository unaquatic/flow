package ui

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/input"
)

// The tests take the levels and variants of the models from testCatalog:
// model-a takes none through max and has a fast tier and a pro mode, and
// model-c takes minimal through high.

// toasts returns the texts of the notices that out adds.
func toasts(out []UIEvent) []string {
	var texts []string
	for _, ev := range out {
		if toast, ok := toastOf(ev); ok {
			texts = append(texts, cmp.Or(toast.Text, JoinStyledLines(toast.Lines).PlainText()))
		}
	}
	return texts
}

// openModelMenu opens the model menu of s on a listing of model-c, model-a
// with fast and pro modes, and model, which takes no levels, and returns
// the banner it shows.
func openModelMenu(t *testing.T, s *Service) EventBannerShow {
	t.Helper()
	s.OpenBanner(ModelBanner)
	return bannerShown(t, s.ModelsListed([]app.ModelListing{listing(s.app.Models().Catalog(), providerA, "model-c", "model-a", "model-a-pro", "model")}))
}

// TestModelMenuEffortPage checks that choosing a model that takes levels
// shows them next, under the title "Reasoning effort", marking the default
// and selecting the level it would run at, and that choosing one switches to
// the model at that level and says so.
func TestModelMenuEffortPage(t *testing.T) {
	s, _ := newTestService(t)
	openModelMenu(t, s)

	page := bannerShown(t, s.HandleBannerSelect(2))
	if page.Title != "Reasoning effort" {
		t.Errorf("page title = %q, want Reasoning effort", page.Title)
	}
	want := []string{"None", "Low", "Medium", "High", "Xhigh", "Max"}
	if got := bannerNames(page); !slices.Equal(got, want) {
		t.Errorf("page lists %q, want %q", got, want)
	}
	if page.SelectedIdx != 2 {
		t.Errorf("page selects %d, want medium, the default, at 2", page.SelectedIdx)
	}
	for i, item := range page.Items {
		if marked := item.Hint == DefaultHint; marked != (item.Name == "Medium") {
			t.Errorf("row %d %q marked default %v, want only medium marked", i, item.Name, marked)
		}
	}
	if s.state.Model != "provider-a/model" {
		t.Errorf("model = %q before a level is chosen, want provider-a/model as it was", s.state.Model)
	}

	out := s.HandleBannerSelect(3)
	if s.state.Model != "provider-a/model-a:fast:high" || s.state.Banner != NoBanner {
		t.Errorf("after choosing high: model %q, banner %v, want provider-a/model-a:fast:high and the menu closed", s.state.Model, s.state.Banner)
	}
	if got := toasts(out); !slices.Equal(got, []string{"Switched to provider-a/model-a:fast:high"}) {
		t.Errorf("notices = %q, want Switched to provider-a/model-a:fast:high", got)
	}

	// The next model's page selects the chosen level, as near as it takes it.
	openModelMenu(t, s)
	page = bannerShown(t, s.HandleBannerSelect(0))
	if got := bannerNames(page); page.SelectedIdx >= len(got) || got[page.SelectedIdx] != "High" {
		t.Errorf("model-c page selects %d of %q, want high", page.SelectedIdx, got)
	}
}

// TestModelMenuWithoutEfforts checks that choosing a model that takes no
// levels switches to it at once.
func TestModelMenuWithoutEfforts(t *testing.T) {
	s, _ := newTestService(t)
	s.SelectModel("provider-a/model-a:high")
	openModelMenu(t, s)

	out := s.HandleBannerSelect(4)
	if s.state.Model != "provider-a/model" || s.state.Banner != NoBanner {
		t.Errorf("model %q, banner %v, want provider-a/model and the menu closed", s.state.Model, s.state.Banner)
	}
	if got := toasts(out); !slices.Equal(got, []string{"Switched to provider-a/model"}) {
		t.Errorf("notices = %q, want Switched to provider-a/model", got)
	}
}

// TestEffortCommand checks that /effort lists the levels of the terminal's
// model under the title "Reasoning effort", selecting the one it runs at, and
// that choosing one switches the same model and mode to that level and says
// so.
func TestEffortCommand(t *testing.T) {
	s, _ := newTestService(t)
	s.SelectModel("provider-a/model-a:fast:xhigh")

	page := bannerShown(t, s.HandleSubmit("/effort"))
	if page.Title != "Reasoning effort" {
		t.Errorf("page title = %q, want Reasoning effort", page.Title)
	}
	want := []string{"None", "Low", "Medium", "High", "Xhigh", "Max"}
	if got := bannerNames(page); !slices.Equal(got, want) {
		t.Errorf("page lists %q, want %q", got, want)
	}
	if page.SelectedIdx != 4 {
		t.Errorf("page selects %d, want xhigh, the level it runs at, at 4", page.SelectedIdx)
	}

	out := s.HandleBannerSelect(1)
	if s.state.Model != "provider-a/model-a:fast:low" || s.state.Banner != NoBanner {
		t.Errorf("after choosing low: model %q, banner %v, want provider-a/model-a:fast:low and the menu closed", s.state.Model, s.state.Banner)
	}
	if got := toasts(out); !slices.Equal(got, []string{"Switched to provider-a/model-a:fast:low"}) {
		t.Errorf("notices = %q, want Switched to provider-a/model-a:fast:low", got)
	}
}

// TestEffortCommandWithoutEfforts checks that /effort on a model that takes
// no levels opens no menu, and says why.
func TestEffortCommandWithoutEfforts(t *testing.T) {
	s, _ := newTestService(t)
	out := s.HandleSubmit("/effort")
	if got := toasts(out); !slices.Equal(got, []string{"Selected model provider-a/model has no reasoning efforts."}) {
		t.Errorf("notices = %q, want that provider-a/model has no reasoning efforts", got)
	}
	if s.state.Banner != NoBanner || s.state.Model != "provider-a/model" {
		t.Errorf("banner %v, model %q, want no banner and provider-a/model", s.state.Banner, s.state.Model)
	}
}

// TestModelNoticesMarkTheModel checks that the notices naming a model draw it,
// and only it, in the product's primary color.
func TestModelNoticesMarkTheModel(t *testing.T) {
	s, _ := newTestService(t)
	for name, out := range map[string][]UIEvent{
		"no efforts": s.noEffort(),
		"switched":   s.SelectModel("provider-a/model"),
	} {
		var marked []string
		for _, ev := range out {
			if toast, ok := toastOf(ev); ok {
				for _, line := range toast.Lines {
					for _, c := range line.Chunks {
						if c.Fg == productFg {
							marked = append(marked, c.Text)
						}
					}
				}
			}
		}
		if !slices.Equal(marked, []string{"provider-a/model"}) {
			t.Errorf("%s: text in the primary color = %q, want the model alone", name, marked)
		}
	}
}

// TestCycleWithoutEfforts checks that Shift+Tab on a model that takes no
// levels leaves it as it is, and says why.
func TestCycleWithoutEfforts(t *testing.T) {
	s, _ := newTestService(t)
	out := s.CycleEffort()
	if got := toasts(out); !slices.Equal(got, []string{"Selected model provider-a/model has no reasoning efforts."}) {
		t.Errorf("notices = %q, want that provider-a/model has no reasoning efforts", got)
	}
	if s.state.Model != "provider-a/model" {
		t.Errorf("model = %q, want provider-a/model", s.state.Model)
	}
}

// TestCycleEffort checks that Shift+Tab steps through the levels the model
// takes, from the default back round to the first, updating the label
// without a notice.
func TestCycleEffort(t *testing.T) {
	s, _ := newTestService(t)
	s.SelectModel("provider-a/model-a:fast")
	if s.state.Model != "provider-a/model-a:fast:medium" {
		t.Fatalf("model = %q, want provider-a/model-a:fast:medium, the default level", s.state.Model)
	}

	var got []string
	for range 8 {
		out := s.CycleEffort()
		if n := toasts(out); n != nil {
			t.Errorf("cycling added notices %q", n)
		}
		if s.state.ModelLabel != s.state.Model {
			t.Errorf("label %q, want the model %q", s.state.ModelLabel, s.state.Model)
		}
		got = append(got, s.state.Model)
	}
	want := []string{
		"provider-a/model-a:fast:high", "provider-a/model-a:fast:xhigh", "provider-a/model-a:fast:max",
		"provider-a/model-a:fast:none", "provider-a/model-a:fast:low", "provider-a/model-a:fast:medium",
		"provider-a/model-a:fast:high", "provider-a/model-a:fast:xhigh",
	}
	if !slices.Equal(got, want) {
		t.Errorf("cycle = %q\nwant    %q", got, want)
	}
}

// TestSelectModelKeepsEffort checks that a model switch keeps the chosen
// level where the new model lists it, runs a model that does not at its
// provider's default, and keeps the chosen level for the next model.
func TestSelectModelKeepsEffort(t *testing.T) {
	s, _ := newTestService(t)
	steps := []struct{ selected, want string }{
		{"provider-a/model-a:max", "provider-a/model-a:max"},
		{"provider-a/model-c", "provider-a/model-c:medium"},
		{"provider-a/model-a-pro", "provider-a/model-a-pro:max"},
		{"provider-a/model", "provider-a/model"},
		{"provider-a/model-a", "provider-a/model-a:max"},
		{"provider-a/model-c:low", "provider-a/model-c:low"},
		{"provider-a/model-a", "provider-a/model-a:low"},
	}
	for _, step := range steps {
		out := s.SelectModel(step.selected)
		if s.state.Model != step.want {
			t.Errorf("select %q: model %q, want %q", step.selected, s.state.Model, step.want)
		}
		if got := toasts(out); !slices.Equal(got, []string{"Switched to " + step.want}) {
			t.Errorf("select %q: notices %q, want Switched to %s", step.selected, got, step.want)
		}
	}
}

// TestModelMenuListsModes checks that each model is followed by its modes,
// and that the row of the terminal's model and mode is selected whatever
// its level.
func TestModelMenuListsModes(t *testing.T) {
	s, _ := newTestService(t)
	s.SelectModel("provider-a/model-a:fast:high")
	show := openModelMenu(t, s)
	want := []string{"model-c [provider-a]", "model-a [provider-a]", "model-a:fast [provider-a]", "model-a-pro [provider-a]", "model [provider-a]"}
	if got := bannerRows(show); !slices.Equal(got, want) {
		t.Errorf("menu lists %q, want %q", got, want)
	}
	if show.SelectedIdx != 2 {
		t.Errorf("menu selects %d, want provider-a/model-a:fast at 2", show.SelectedIdx)
	}
}

// storeVariantMessage stores a message of session id that model variant v
// answered.
func storeVariantMessage(t *testing.T, a *app.App, id message.SessionID, v agent.Variant) {
	t.Helper()
	err := a.Sessions().AppendMessage(t.Context(), id, &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleAgent,
		Parts: []message.ContentPart{message.TextPart{Text: "hello"}},
		Model: v.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestResumeRestoresModel checks that a resumed session brings back the
// model variant it last ran, unless the command line chose the terminal's
// model for the first session.
func TestResumeRestoresModel(t *testing.T) {
	ran := agent.Variant{Provider: providerA, Model: "provider-a/model-a", Option: "fast", Effort: "high"}
	t.Run("resume", func(t *testing.T) {
		s, a := newTestService(t)
		id := message.NewSessionID()
		storeVariantMessage(t, a, id, agent.Variant{Provider: providerA, Model: "provider-a/model-c"})
		storeVariantMessage(t, a, id, ran)
		s.NewSession()
		s.HandleSessionSelect(id)
		if s.state.Model != ran.String() || s.state.ModelLabel != ran.String() || s.state.ReasoningEffort != "high" {
			t.Errorf("model, label, level = %q, %q, %q, want %s at high", s.state.Model, s.state.ModelLabel, s.state.ReasoningEffort, ran)
		}
	})
	t.Run("first session", func(t *testing.T) {
		s, a := newTestService(t)
		id := message.NewSessionID()
		storeVariantMessage(t, a, id, ran)
		s.InitSession(t.Context(), id)
		if s.state.Model != ran.String() {
			t.Errorf("model = %q, want %s", s.state.Model, ran)
		}
	})
	t.Run("first session keeps the command line's", func(t *testing.T) {
		s, a := newTestService(t)
		id := message.NewSessionID()
		storeVariantMessage(t, a, id, ran)
		s.state.KeepModel = true
		s.InitSession(t.Context(), id)
		if s.state.Model != "provider-a/model" {
			t.Errorf("model = %q, want provider-a/model as the command line chose", s.state.Model)
		}
	})
}

// TestSessionUsagePricesModes checks that a reply is priced at the price of
// the variant its model ran as, named as the catalog names it or as flow
// named a mode before.
func TestSessionUsagePricesModes(t *testing.T) {
	s, a := newTestService(t)
	base, okBase := a.Models().Catalog().Resolve("provider-a", "provider-a/model-a")
	fast, okFast := a.Models().Catalog().Resolve("provider-a", "provider-a/model-a:fast")
	if !okBase || !okFast || base.Price == nil || fast.Price == nil || fast.Price.Input == base.Price.Input {
		t.Fatal("the catalog lists no fast price for provider-a/model-a")
	}
	u := message.Usage{InputTokens: 10_000, OutputTokens: 1_000, TotalTokens: 11_000}
	s.state.Messages = []ChatMessage{
		{ID: "a1", Role: message.RoleAgent, Model: "provider-a/model-a", Usage: u},
		{ID: "a2", Role: message.RoleAgent, Model: "provider-a/model-a:fast:high", Usage: u},
		{ID: "a3", Role: message.RoleAgent, Model: "provider-a/model-a:fast:high", Usage: u},
	}
	got := s.SessionUsage().Cost
	want := base.Price.At(10_000).Charge(10_000, 0, 0, 1_000) + 2*fast.Price.At(10_000).Charge(10_000, 0, 0, 1_000)
	if got != want {
		t.Errorf("cost = %v, want %v", got, want)
	}
}

// TestShiftTabCyclesEffort checks that Shift+Tab, outside menus, cycles
// the reasoning effort and keeps the key from the input, while Tab alone goes
// to the input.
func TestShiftTabCyclesEffort(t *testing.T) {
	ui := newInputTestUI(t)
	ui.Apply(ui.service.SelectModel("provider-a/model-a"))

	tab := &input.KeyEvent{Name: input.KeyTab}
	ui.HandleKey(tab)
	if tab.DefaultPrevented() || ui.service.state.Model != "provider-a/model-a:medium" {
		t.Errorf("Tab: prevented %v, model %q, want the key passed on and the model as it was", tab.DefaultPrevented(), ui.service.state.Model)
	}

	shiftTab := &input.KeyEvent{Name: input.KeyTab, Mods: input.ModShift}
	ui.HandleKey(shiftTab)
	if !shiftTab.DefaultPrevented() || ui.service.state.Model != "provider-a/model-a:high" {
		t.Errorf("Shift+Tab: prevented %v, model %q, want the key kept and provider-a/model-a:high", shiftTab.DefaultPrevented(), ui.service.state.Model)
	}
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	if got := ui.slashStatus.PlainText(); !strings.HasPrefix(got, "model-a high context ") {
		t.Errorf("status row = %q, want it to name model-a high first", got)
	}
}

// TestStartsAtDefaultEffort checks that the terminal's starting model runs
// at its default level when it names none, the catalog's, as model-f's
// high, or else medium, where the catalog lists it for the model, and
// keeps a level it names.
func TestStartsAtDefaultEffort(t *testing.T) {
	_, a := newTestService(t)
	tests := []struct{ start, want string }{
		{"provider-a/model-a", "provider-a/model-a:medium"},
		{"provider-a/model-a:xhigh", "provider-a/model-a:xhigh"},
		// model-c-pro lists high alone, not the default.
		{"provider-a/model-c-pro", "provider-a/model-c-pro"},
		// A model that takes no levels names none.
		{"provider-a/model", "provider-a/model"},
		// The catalog gives model-f high; model-e lists no levels.
		{"provider-b/model-f", "provider-b/model-f:high"},
		{"provider-b/model-e", "provider-b/model-e"},
	}
	for _, tt := range tests {
		state := &UIState{Model: tt.start}
		NewService(t.Context(), a, state)
		if state.Model != tt.want || state.ModelLabel != tt.want {
			t.Errorf("start %q: model, label = %q, %q, want %q", tt.start, state.Model, state.ModelLabel, tt.want)
		}
	}
}

// TestEffortPageMarksTheDefault checks that the effort page marks the
// model's default: the catalog's high for model-f.
func TestEffortPageMarksTheDefault(t *testing.T) {
	s, _ := newTestService(t)
	page := bannerShown(t, s.ShowEffortMenu(agent.Variant{Provider: providerB, Model: "model-f"}))
	for _, item := range page.Items {
		if marked := item.Hint == DefaultHint; marked != (item.Name == "High") {
			t.Errorf("row %q marked default %v, want only high marked", item.Name, marked)
		}
	}
}
