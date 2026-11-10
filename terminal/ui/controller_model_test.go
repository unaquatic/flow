package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
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

// bannerRows returns the banner's items as their rows read, with the space
// between their columns collapsed to one: the name, the hint and then the
// description, such as "Model C provider-a/model-c [provider-a]" in the
// model menu.
func bannerRows(show EventBannerShow) []string {
	var rows []string
	for _, item := range show.Items {
		rows = append(rows, strings.Join(strings.Fields(item.Name+" "+item.Hint+" "+item.Description), " "))
	}
	return rows
}

// listModels opens the model menu of s on a fixed listing instead of
// fetching one, and returns the banner it shows.
func listModels(t *testing.T, s *Service) EventBannerShow {
	t.Helper()
	s.OpenBanner(ModelBanner)
	return bannerShown(t, s.ModelsListed([]app.ModelListing{
		listing(s.app.Models().Catalog(), providerA, "model-c", "model", "model-a4-mini"),
		listing(s.app.Models().Catalog(), providerB, "model-3-7-small", "model-small-4-6"),
	}))
}

func TestModelMenuListsAll(t *testing.T) {
	s, _ := newTestService(t)
	show := listModels(t, s)

	want := []string{"Model C provider-a/model-c [provider-a]", "model model [provider-a]", "model-a4-mini model-a4-mini [provider-a]", "model-3-7-small model-3-7-small [provider-b]", "model-small-4-6 model-small-4-6 [provider-b]"}
	if got := bannerRows(show); !slices.Equal(got, want) {
		t.Errorf("menu lists %q, want %q", got, want)
	}
	if !show.Search {
		t.Error("menu has no search field")
	}
	if show.SelectedIdx != 1 {
		t.Errorf("menu selects %d, want the active provider-a/model at 1", show.SelectedIdx)
	}
}

func TestModelMenuSearch(t *testing.T) {
	tests := []struct {
		name, query string
		want        []string
		wantIdx     int
		wantText    string
	}{
		{name: "later word", query: "small", want: []string{"model-small-4-6 model-small-4-6 [provider-b]", "model-3-7-small model-3-7-small [provider-b]"}},
		{name: "case ignored", query: "MC", want: []string{"Model C provider-a/model-c [provider-a]"}},
		{name: "typo ties keep listing order", query: "smlal", want: []string{"model-3-7-small model-3-7-small [provider-b]", "model-small-4-6 model-small-4-6 [provider-b]"}},
		{name: "cleared", query: " ", want: []string{"Model C provider-a/model-c [provider-a]", "model model [provider-a]", "model-a4-mini model-a4-mini [provider-a]", "model-3-7-small model-3-7-small [provider-b]", "model-small-4-6 model-small-4-6 [provider-b]"}, wantIdx: 1},
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

// TestModelMenuRows checks that the model menu names each row by the name
// the catalog gives its variant, followed by the variant's ID and its
// provider, and finds a row by its ID and its whole "provider/model" form
// too.
func TestModelMenuRows(t *testing.T) {
	s, _ := newTestService(t)
	s.OpenBanner(ModelBanner)
	show := bannerShown(t, s.ModelsListed([]app.ModelListing{
		listing(s.app.Models().Catalog(), providerA, "model-b"),
		listing(s.app.Models().Catalog(), providerB, "model-g"),
	}))
	want := []string{"Model B provider-a/model-b [provider-a]", "Model B Fast provider-a/model-b:fast [provider-a]", "Model G provider-b/model-g [provider-b]"}
	if got := bannerRows(show); !slices.Equal(got, want) {
		t.Errorf("menu lists %q, want %q", got, want)
	}
	for query, want := range map[string]string{
		"b:fast":                             "Model B Fast provider-a/model-b:fast [provider-a]",
		"provider-a/provider-a/model-b:fast": "Model B Fast provider-a/model-b:fast [provider-a]",
		"provider-b/":                        "Model G provider-b/model-g [provider-b]",
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
	bannerShown(t, s.HandleBannerSearch("provider-b/provider-b/model-y"))

	custom, out := s.HandleBannerCustom()
	if !custom || s.state.Banner != NoBanner || s.state.Prompt == nil {
		t.Fatalf("custom = %v, banner %v, prompt %v; want the menu closed and a question open", custom, s.state.Banner, s.state.Prompt)
	}
	if !slices.Contains(out, UIEvent(EventInputSet{Text: "provider-b/provider-b/model-y"})) {
		t.Errorf("events %v do not keep the search in the input", out)
	}
	if got := s.InputHint(time.Now()); got != CustomModelPlaceholder {
		t.Errorf("input hint = %q, want %q", got, CustomModelPlaceholder)
	}

	s.HandleSubmit("provider-b/provider-b/model-y")
	if s.state.Model != "provider-b/provider-b/model-y" || s.state.Prompt != nil {
		t.Errorf("after the answer: model %q, prompt %v; want provider-b/provider-b/model-y and none", s.state.Model, s.state.Prompt)
	}
	if got := s.InputHint(time.Now()); got != "" {
		t.Errorf("input hint after the answer = %q, want none", got)
	}

	settings := &Service{state: &UIState{}}
	if show := bannerShown(t, settings.HandleSubmit("/config")); slices.Contains(show.Hints(), HintCustom) {
		t.Errorf("settings keys %v name %v", show.Hints(), HintCustom)
	}
	if custom, _ := settings.HandleBannerCustom(); custom {
		t.Error("Tab enters a custom entry in /config")
	}
}

func TestModelMenuSearchAfterClose(t *testing.T) {
	s, _ := newTestService(t)
	listModels(t, s)
	s.CancelBanner()

	if out := s.HandleBannerSearch("model"); out != nil {
		t.Errorf("search after close = %v, want nothing", out)
	}
}

// TestSelectModelSavesTheDefault checks that choosing a model changes the
// model this terminal labels and sends its prompts to, and saves it as the
// app's default model, saying so in one notice.
func TestSelectModelSavesTheDefault(t *testing.T) {
	q := newQueueTest(t)
	out := q.s.SelectModel("provider-b/provider-b/model-y")

	if q.s.state.Model != "provider-b/provider-b/model-y" || q.s.state.ModelLabel != "provider-b/provider-b/model-y" {
		t.Errorf("terminal model, label = %q, %q, want provider-b/provider-b/model-y", q.s.state.Model, q.s.state.ModelLabel)
	}
	if got, err := q.a.Models().Default(); got != "provider-b/provider-b/model-y" || err != nil {
		t.Errorf("app default model = %q, %v, want provider-b/provider-b/model-y saved", got, err)
	}
	if got := toasts(out); !slices.Equal(got, []string{"Switched to provider-b/provider-b/model-y as the default model"}) {
		t.Errorf("notices = %q, want the model switched to and saved", got)
	}

	if err := q.s.SendPrompt("hi"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	started := q.until(app.TypeMessageStarted).(app.EventMessageStarted)
	if m := started.Message; agent.ParseVariantAt(m.Provider, m.Model, q.a.Models().Catalog()).String() != "provider-b/provider-b/model-y" {
		t.Errorf("prompt sent to %s at %s, want provider-b/provider-b/model-y", m.Model, m.Provider)
	}
	q.until(app.TypeAgentEnded)
}

// TestModelMenuChooseSavesTheDefault checks that choosing a model in the
// model menu, and a level on its levels page, saves the model at that level
// as the app's default model and closes the menu, and that neither page
// names a key to save a row apart from choosing it.
func TestModelMenuChooseSavesTheDefault(t *testing.T) {
	s, _ := newTestService(t)
	show := listModels(t, s)
	noSaveKey := func(show EventBannerShow) {
		t.Helper()
		for _, h := range show.Hints() {
			if h.Key == "ctrl+s" {
				t.Errorf("menu %q names %v", show.Title, h)
			}
		}
	}
	noSaveKey(show)

	levels := bannerShown(t, s.HandleBannerSelect(slices.Index(bannerNames(show), "Model C")))
	noSaveKey(levels)
	out := s.HandleBannerSelect(slices.Index(bannerNames(levels), "High"))
	if got, err := s.app.Models().Default(); got != "provider-a/provider-a/model-c:high" || err != nil {
		t.Errorf("app default model = %q, %v, want provider-a/provider-a/model-c:high", got, err)
	}
	if got := toasts(out); !slices.Equal(got, []string{"Switched to provider-a/provider-a/model-c:high as the default model"}) {
		t.Errorf("notices = %q, want the model switched to and saved", got)
	}
	if s.state.Banner != NoBanner || s.state.Model != "provider-a/provider-a/model-c:high" {
		t.Errorf("banner %v, model %q, want the menu closed and provider-a/provider-a/model-c:high", s.state.Banner, s.state.Model)
	}
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
		toast, ok := toastOf(ev)
		return ok && toast.Text == "Listing models failed: boom"
	}) {
		t.Errorf("events = %v, want a toast with the reason", out)
	}
	if s.state.Banner == ModelBanner {
		t.Error("the loading banner stayed open")
	}
}
