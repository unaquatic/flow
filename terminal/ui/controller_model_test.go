package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
)

// bannerShown returns the banner that out shows.
func bannerShown(t *testing.T, out []UIEvent) EventBannerShow {
	t.Helper()
	for _, ev := range out {
		if show, ok := ev.(EventBannerShow); ok {
			return show
		}
	}
	t.Fatalf("events %v show no banner", out)
	return EventBannerShow{}
}

// bannerNames returns the names of the banner's items.
func bannerNames(show EventBannerShow) []string {
	var names []string
	for _, item := range show.Items {
		names = append(names, item.Name)
	}
	return names
}

// bannerRows returns the banner's items as their rows read: the name and
// then the hint, such as "gpt-5 [openai]" in the model menu.
func bannerRows(show EventBannerShow) []string {
	var rows []string
	for _, item := range show.Items {
		rows = append(rows, strings.TrimSpace(item.Name+" "+item.Hint))
	}
	return rows
}

// listModels opens the model menu of s on a fixed listing instead of
// fetching one, and returns the banner it shows.
func listModels(t *testing.T, s *Service) EventBannerShow {
	t.Helper()
	s.OpenBanner(ModelBanner)
	return bannerShown(t, s.ModelsListed([]app.ModelListing{
		listing(s.app.Models().Catalog(), chatgpt.ProviderID, "gpt-5", "model", "o4-mini"),
		listing(s.app.Models().Catalog(), claude.ProviderID, "claude-3-7-sonnet", "claude-sonnet-4-5"),
	}))
}

func TestModelMenuListsAll(t *testing.T) {
	s, _ := newTestService(t)
	show := listModels(t, s)

	want := []string{"gpt-5 [openai]", "model [openai]", "o4-mini [openai]", "claude-3-7-sonnet [anthropic]", "claude-sonnet-4-5 [anthropic]"}
	if got := bannerRows(show); !slices.Equal(got, want) {
		t.Errorf("menu lists %q, want %q", got, want)
	}
	if !show.Search {
		t.Error("menu has no search field")
	}
	if show.SelectedIdx != 1 {
		t.Errorf("menu selects %d, want the active openai/model at 1", show.SelectedIdx)
	}
}

func TestModelMenuSearch(t *testing.T) {
	tests := []struct {
		name, query string
		want        []string
		wantIdx     int
		wantText    string
	}{
		{name: "later word", query: "sonnet", want: []string{"claude-sonnet-4-5 [anthropic]", "claude-3-7-sonnet [anthropic]"}},
		{name: "case ignored", query: "GPT5", want: []string{"gpt-5 [openai]"}},
		{name: "typo ties keep listing order", query: "sonnte", want: []string{"claude-3-7-sonnet [anthropic]", "claude-sonnet-4-5 [anthropic]"}},
		{name: "cleared", query: " ", want: []string{"gpt-5 [openai]", "model [openai]", "o4-mini [openai]", "claude-3-7-sonnet [anthropic]", "claude-sonnet-4-5 [anthropic]"}, wantIdx: 1},
		{name: "no match", query: "zzz", wantText: NoMatchesText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService(t)
			listModels(t, s)

			show := bannerShown(t, s.HandleBannerSearch(tt.query))
			if got := bannerRows(show); !slices.Equal(got, tt.want) {
				t.Errorf("search %q lists %q, want %q", tt.query, got, tt.want)
			}
			if show.SelectedIdx != tt.wantIdx {
				t.Errorf("search %q selects %d, want %d", tt.query, show.SelectedIdx, tt.wantIdx)
			}
			if show.Text != tt.wantText {
				t.Errorf("search %q shows text %q, want %q", tt.query, show.Text, tt.wantText)
			}
		})
	}
}

// TestModelMenuRows checks that the model menu names each row by its
// variant, as the catalog's ID names it without the lab when the provider
// is the lab, and not by the name the catalog gives it, followed by the
// provider as a hint, and finds a row by its whole ID too.
func TestModelMenuRows(t *testing.T) {
	s, _ := newTestService(t)
	s.OpenBanner(ModelBanner)
	show := bannerShown(t, s.ModelsListed([]app.ModelListing{
		listing(s.app.Models().Catalog(), chatgpt.ProviderID, "gpt-5.6-luna"),
		listing(s.app.Models().Catalog(), claude.ProviderID, "claude-opus-5-5"),
	}))
	want := []string{"gpt-5.6-luna [openai]", "gpt-5.6-luna:fast [openai]", "claude-opus-5-5 [anthropic]"}
	if got := bannerRows(show); !slices.Equal(got, want) {
		t.Errorf("menu lists %q, want %q", got, want)
	}
	for query, want := range map[string]string{
		"luna:fast":                "gpt-5.6-luna:fast [openai]",
		"openai/gpt-5.6-luna:fast": "gpt-5.6-luna:fast [openai]",
		"anthropic/":               "claude-opus-5-5 [anthropic]",
	} {
		show := bannerShown(t, s.HandleBannerSearch(query))
		if got := bannerRows(show); len(got) == 0 || got[0] != want {
			t.Errorf("search %q lists %q, want %s first", query, got, want)
		}
	}
}

// TestModelMenuCustomModel checks that the model menu names Tab as its key
// for a model it does not list, which closes the menu and asks for one, with
// the search in the input and CustomModelPlaceholder as the input's hint, and
// that the answer switches to the model and takes the hint away. Other menus
// name no such key.
func TestModelMenuCustomModel(t *testing.T) {
	s, _ := newTestService(t)
	if show := listModels(t, s); !slices.Contains(show.Hints(), HintCustom) {
		t.Errorf("model menu keys %v lack %v", show.Hints(), HintCustom)
	}
	bannerShown(t, s.HandleBannerSearch("anthropic/claude-x"))

	custom, out := s.HandleBannerCustom()
	if !custom || s.state.Banner != NoBanner || s.state.Prompt == nil {
		t.Fatalf("custom = %v, banner %v, prompt %v; want the menu closed and a question open", custom, s.state.Banner, s.state.Prompt)
	}
	if !slices.Contains(out, UIEvent(EventInputSet{Text: "anthropic/claude-x"})) {
		t.Errorf("events %v do not keep the search in the input", out)
	}
	if got := s.InputHint(time.Now()); got != CustomModelPlaceholder {
		t.Errorf("input hint = %q, want %q", got, CustomModelPlaceholder)
	}

	s.HandleSubmit("anthropic/claude-x")
	if s.state.Model != "anthropic/claude-x" || s.state.Prompt != nil {
		t.Errorf("after the answer: model %q, prompt %v; want anthropic/claude-x and none", s.state.Model, s.state.Prompt)
	}
	if got := s.InputHint(time.Now()); got != "" {
		t.Errorf("input hint after the answer = %q, want none", got)
	}

	settings := &Service{state: &UIState{}}
	if show := bannerShown(t, settings.HandleSubmit("/settings")); slices.Contains(show.Hints(), HintCustom) {
		t.Errorf("settings keys %v name %v", show.Hints(), HintCustom)
	}
	if custom, _ := settings.HandleBannerCustom(); custom {
		t.Error("Tab enters a custom entry in /settings")
	}
}

func TestModelMenuSearchAfterClose(t *testing.T) {
	s, _ := newTestService(t)
	listModels(t, s)
	s.CancelBanner()

	if out := s.HandleBannerSearch("gpt"); out != nil {
		t.Errorf("search after close = %v, want nothing", out)
	}
}

// TestSelectModelIsTheTerminals checks that choosing a model changes the
// model this terminal labels and sends its prompts to, and leaves the app's
// default model as it was.
func TestSelectModelIsTheTerminals(t *testing.T) {
	q := newQueueTest(t)
	q.s.SelectModel("anthropic/claude-x")

	if q.s.state.Model != "anthropic/claude-x" || q.s.state.ModelLabel != "anthropic/claude-x" {
		t.Errorf("terminal model, label = %q, %q, want anthropic/claude-x", q.s.state.Model, q.s.state.ModelLabel)
	}
	if got, err := q.a.Models().Default(); got != "openai/model" || err != nil {
		t.Errorf("app default model = %q, %v, want openai/model as it was", got, err)
	}

	if err := q.s.SendPrompt("hi"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	started := q.until(app.TypeMessageStarted).(app.EventMessageStarted)
	if m := started.Message; m.Model != "anthropic/claude-x" {
		t.Errorf("prompt sent to %s, want anthropic/claude-x", m.Model)
	}
	q.until(app.TypeAgentEnded)
}

// TestModelsListFailed checks that a failed listing closes the loading
// banner and shows why, and does nothing once the banner has closed.
func TestModelsListFailed(t *testing.T) {
	s, _ := newTestService(t)
	if out := s.ModelsListFailed("boom"); out != nil {
		t.Errorf("ModelsListFailed without the loading banner = %v, want nothing", out)
	}

	s.OpenBanner(ModelBanner)
	out := s.ModelsListFailed("boom")
	if !slices.ContainsFunc(out, func(ev UIEvent) bool {
		toast, ok := ev.(EventToastShow)
		return ok && toast.Text == "Listing models failed: boom"
	}) {
		t.Errorf("events = %v, want a toast with the reason", out)
	}
	if s.state.Banner == ModelBanner {
		t.Error("the loading banner stayed open")
	}
}
