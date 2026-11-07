package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

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

// bannerRows returns the banner's items as their rows read: the name and
// then the hint, such as "model-c [provider-a]" in the model menu.
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
		listing(s.app.Models().Catalog(), providerA, "model-c", "model", "model-a4-mini"),
		listing(s.app.Models().Catalog(), providerB, "model-3-7-small", "model-small-4-6"),
	}))
}

func TestModelMenuListsAll(t *testing.T) {
	s, _ := newTestService(t)
	show := listModels(t, s)

	want := []string{"model-c [provider-a]", "model [provider-a]", "model-a4-mini [provider-a]", "model-3-7-small [provider-b]", "model-small-4-6 [provider-b]"}
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
		{name: "later word", query: "small", want: []string{"model-small-4-6 [provider-b]", "model-3-7-small [provider-b]"}},
		{name: "case ignored", query: "MC", want: []string{"model-c [provider-a]"}},
		{name: "typo ties keep listing order", query: "smlal", want: []string{"model-3-7-small [provider-b]", "model-small-4-6 [provider-b]"}},
		{name: "cleared", query: " ", want: []string{"model-c [provider-a]", "model [provider-a]", "model-a4-mini [provider-a]", "model-3-7-small [provider-b]", "model-small-4-6 [provider-b]"}, wantIdx: 1},
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
		listing(s.app.Models().Catalog(), providerA, "model-b"),
		listing(s.app.Models().Catalog(), providerB, "model-g"),
	}))
	want := []string{"model-b [provider-a]", "model-b:fast [provider-a]", "model-g [provider-b]"}
	if got := bannerRows(show); !slices.Equal(got, want) {
		t.Errorf("menu lists %q, want %q", got, want)
	}
	for query, want := range map[string]string{
		"b:fast":                  "model-b:fast [provider-a]",
		"provider-a/model-b:fast": "model-b:fast [provider-a]",
		"provider-b/":             "model-g [provider-b]",
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
	bannerShown(t, s.HandleBannerSearch("provider-b/model-y"))

	custom, out := s.HandleBannerCustom()
	if !custom || s.state.Banner != NoBanner || s.state.Prompt == nil {
		t.Fatalf("custom = %v, banner %v, prompt %v; want the menu closed and a question open", custom, s.state.Banner, s.state.Prompt)
	}
	if !slices.Contains(out, UIEvent(EventInputSet{Text: "provider-b/model-y"})) {
		t.Errorf("events %v do not keep the search in the input", out)
	}
	if got := s.InputHint(time.Now()); got != CustomModelPlaceholder {
		t.Errorf("input hint = %q, want %q", got, CustomModelPlaceholder)
	}

	s.HandleSubmit("provider-b/model-y")
	if s.state.Model != "provider-b/model-y" || s.state.Prompt != nil {
		t.Errorf("after the answer: model %q, prompt %v; want provider-b/model-y and none", s.state.Model, s.state.Prompt)
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

	if out := s.HandleBannerSearch("model"); out != nil {
		t.Errorf("search after close = %v, want nothing", out)
	}
}

// TestSelectModelIsTheTerminals checks that choosing a model changes the
// model this terminal labels and sends its prompts to, and leaves the app's
// default model as it was.
func TestSelectModelIsTheTerminals(t *testing.T) {
	q := newQueueTest(t)
	q.s.SelectModel("provider-b/model-y")

	if q.s.state.Model != "provider-b/model-y" || q.s.state.ModelLabel != "provider-b/model-y" {
		t.Errorf("terminal model, label = %q, %q, want provider-b/model-y", q.s.state.Model, q.s.state.ModelLabel)
	}
	if got, err := q.a.Models().Default(); got != "provider-a/model" || err != nil {
		t.Errorf("app default model = %q, %v, want provider-a/model as it was", got, err)
	}

	if err := q.s.SendPrompt("hi"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	started := q.until(app.TypeMessageStarted).(app.EventMessageStarted)
	if m := started.Message; m.Model != "provider-b/model-y" {
		t.Errorf("prompt sent to %s, want provider-b/model-y", m.Model)
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
		toast, ok := toastOf(ev)
		return ok && toast.Text == "Listing models failed: boom"
	}) {
		t.Errorf("events = %v, want a toast with the reason", out)
	}
	if s.state.Banner == ModelBanner {
		t.Error("the loading banner stayed open")
	}
}
