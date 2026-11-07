package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/extension/protocol/login/claude"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/style"
)

// newTestService returns a Service over an isolated app whose model is
// openai/model and in which no API key is set in the environment.
func newTestService(t *testing.T, loads ...extension.Load) (*Service, *app.App) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	home := t.TempDir()
	settings := `{"model":"openai/model","snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestCatalog(t, home)
	conf := config.NewService(home)
	a, err := app.New(t.Context(), conf, app.Options{Extensions: append([]extension.Load{protocol.Load}, loads...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	model, err := a.Models().Default()
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(t.Context(), a, &UIState{Model: model})
	s.Commands = builtinCommands(t)
	return s, a
}

// LoginTestExtension supplies an isolated login flow for UI lifecycle tests.
type LoginTestExtension struct {
	// Flow is the Claude login under test.
	Flow agent.LoginFlow
}

// Name identifies the test extension.
func (e *LoginTestExtension) Name() string { return "test_login" }

// LoginFlows overrides Claude sign-in for the test app.
func (e *LoginTestExtension) LoginFlows() map[agent.ProviderID]agent.LoginFlow {
	return map[agent.ProviderID]agent.LoginFlow{claude.ProviderID: e.Flow}
}

func TestManualLoginLifecycle(t *testing.T) {
	for _, action := range []string{"retry", "escape", "ctrl-c"} {
		t.Run(action, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if action != "retry" {
					t.Error("cancelled login attempted a token exchange")
				}
				fmt.Fprint(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
			}))
			defer server.Close()
			oauth := claude.NewOAuth()
			oauth.TokenURL = server.URL
			oauth.OpenBrowser = func(string) error { return nil }
			ui := newInputTestUI(t, func(context.Context, extension.Host) (extension.Extension, error) {
				return &LoginTestExtension{Flow: oauth}, nil
			})
			s, a := ui.service, ui.service.app
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			events := a.Subscribe(ctx)
			if _, err := a.Accounts().StartLogin(claude.ProviderID, "", agent.LoginBrowser); err != nil {
				t.Fatal(err)
			}
			var prompt app.EventLoginPrompt
			for prompt.URL == "" {
				select {
				case ev := <-events:
					if p, ok := ev.(app.EventLoginPrompt); ok {
						prompt = p
					}
				case <-ctx.Done():
					t.Fatal("timed out waiting for login prompt")
				}
			}
			if prompt.State == nil {
				t.Fatal("manual login did not return its temporary proof")
			}
			ui.Apply(s.ApplyAgentEvent(prompt))
			switch action {
			case "retry":
				ui.HandleEnter()
				if s.state.Login == nil || s.state.LoginCompleting {
					t.Fatal("empty input closed the authorization or started a request")
				}
				ui.SetInputText("code-without-state")
				ui.HandleEnter()
				select {
				case ev := <-events:
					failed, ok := ev.(app.EventLoginFailed)
					if !ok || !failed.Retry {
						t.Fatalf("malformed code event = %#v", ev)
					}
					ui.Apply(s.ApplyAgentEvent(failed))
				case <-ctx.Done():
					t.Fatal("malformed code did not return a validation error")
				}
				if s.state.Login == nil || s.state.LoginCompleting || len(s.state.Messages) != 0 {
					t.Fatal("retry lost its proof or added login instructions to chat")
				}
				u, _ := url.Parse(prompt.URL)
				ui.SetInputText("code#" + u.Query().Get("state"))
				ui.HandleEnter()
			case "escape":
				ui.HandleKey(&input.KeyEvent{Name: input.KeyEscape})
			case "ctrl-c":
				ui.HandleKey(&input.KeyEvent{Name: "c", Mods: input.ModCtrl})
			}
			if action != "retry" {
				if s.state.Login != nil || s.state.Banner != NoBanner || len(s.state.Messages) != 0 {
					t.Fatal("cancel did not discard the temporary login state cleanly")
				}
				return
			}
			for {
				select {
				case ev := <-events:
					switch e := ev.(type) {
					case app.EventLoginSucceeded:
						ui.Apply(s.ApplyAgentEvent(e))
						if s.state.Login != nil || len(s.state.Queue) != 0 {
							t.Fatal("completed input retained its proof or queued a model message")
						}
						return
					case app.EventLoginFailed:
						t.Fatalf("unexpected login failure: %v", e.Error)
					}
				case <-ctx.Done():
					t.Fatal("login did not settle after submission or cancellation")
				}
			}
		})
	}
}

// openAIStatus returns the OpenAI accounts of a.
func openAIStatus(t *testing.T, a *app.App) agent.AuthStatus {
	t.Helper()
	st, err := a.Accounts().Status(t.Context(), chatgpt.ProviderID)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// addKey saves an API key as a new OpenAI account of a and returns its ID.
func addKey(t *testing.T, a *app.App) agent.AccountID {
	t.Helper()
	id, err := a.Accounts().SetKey(t.Context(), chatgpt.ProviderID, "", "sk")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// step is one user action and the UI events it produced.
type step func(s *Service) []UIEvent

func submit(text string) step {
	return func(s *Service) []UIEvent { return s.HandleSubmit(text) }
}

func escape(s *Service) []UIEvent {
	_, out := s.HandleEscape()
	return out
}

func ctrlC(s *Service) []UIEvent {
	_, out := s.HandleCtrlC(true)
	return out
}

// choose chooses the row of the open menu named name.
func choose(name string) step {
	return func(s *Service) []UIEvent {
		i := slices.IndexFunc(s.state.Menu.Shown, func(item MenuItem) bool { return item.Name == name })
		return s.HandleBannerSelect(i)
	}
}

// askKey returns the steps that open /login and choose OpenAI, Add account
// and API key, which leaves the API key question open.
func askKey() []step {
	return []step{submit("/login"), choose("openai"), choose(addAccountName), choose(apiKeyName)}
}

// run performs steps and fails the test if one sends the input to the model
// or a question stays open.
func run(t *testing.T, s *Service, steps ...step) {
	t.Helper()
	for i, step := range steps {
		out := step(s)
		if slices.ContainsFunc(out, func(ev UIEvent) bool { _, ok := ev.(EventIndicatorShow); return ok }) {
			t.Fatalf("step %d sent the input to the model", i)
		}
	}
	if s.state.Prompt != nil {
		t.Error("a question is still open")
	}
}

func TestLoginKeyAddsAccount(t *testing.T) {
	s, a := newTestService(t)
	run(t, s, append(askKey(), submit("  sk-test\n"))...)

	st := openAIStatus(t, a)
	if len(st.Accounts) != 1 {
		t.Fatalf("accounts = %+v, want one", st.Accounts)
	}
	acct := st.Accounts[0]
	if _, err := agent.ParseAccountID(string(acct.ID)); err != nil || acct.Kind != agent.CredentialAPIKey || acct.Source != agent.CredentialSourceStore {
		t.Errorf("account = %+v, want a saved API key with a UUID", acct)
	}
	if st.Active != acct.ID {
		t.Errorf("active account = %q, want the new %q", st.Active, acct.ID)
	}
}

func TestLoginKeyCancelled(t *testing.T) {
	tests := []struct {
		name   string
		cancel step
	}{
		{name: "escape", cancel: escape},
		{name: "ctrl+c", cancel: ctrlC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, a := newTestService(t)
			run(t, s, append(askKey(), tt.cancel)...)
			if st := openAIStatus(t, a); len(st.Accounts) != 0 {
				t.Errorf("accounts = %+v, want none", st.Accounts)
			}
		})
	}
}

// accountMenu opens the OpenAI account menu of s and returns its banner and
// the names of its options.
func accountMenu(t *testing.T, s *Service) (EventBannerShow, []string) {
	t.Helper()
	out := s.ShowAccountMenu(chatgpt.ProviderID)
	if len(out) != 1 {
		t.Fatalf("menu = %v, want one banner", out)
	}
	show, ok := out[0].(EventBannerShow)
	if !ok {
		t.Fatalf("menu shows %T, want a banner", out[0])
	}
	var names []string
	for _, item := range show.Items {
		names = append(names, item.Name)
	}
	return show, names
}

func TestLoginMenuSearch(t *testing.T) {
	s, _ := newTestService(t)
	show := bannerShown(t, s.HandleSubmit("/login"))
	all := []string{string(claude.ProviderID), string(chatgpt.ProviderID)}
	if got := bannerNames(show); !show.Search || show.Title != "Login" || !slices.Equal(got, all) {
		t.Fatalf("menu = %+v, want %q under a search field titled Login", show, all)
	}

	tests := []struct {
		query    string
		want     []string
		wantText string
	}{
		{query: "anth", want: []string{string(claude.ProviderID)}},
		{query: "OAI", want: []string{string(chatgpt.ProviderID)}},
		{query: "zzz", wantText: NoMatchesText},
	}
	for _, tt := range tests {
		show := bannerShown(t, s.HandleBannerSearch(tt.query))
		if got := bannerNames(show); !slices.Equal(got, tt.want) || show.Text != tt.wantText {
			t.Errorf("search %q lists %q with text %q, want %q with text %q", tt.query, got, show.Text, tt.want, tt.wantText)
		}
	}

	bannerShown(t, s.HandleBannerSearch(""))
	bannerShown(t, choose("openai")(s))
	if show := bannerShown(t, s.HandleBannerSearch("open")); len(show.Items) != 0 {
		t.Errorf("search in the account menu lists %q, want its own rows, none of which match", bannerNames(show))
	}
}

func TestLoginMenuOmitsAPIKeyProviders(t *testing.T) {
	s, a := newTestService(t, func(_ context.Context, host extension.Host) (extension.Extension, error) {
		gatewaytest.WriteCache(t, host.DataDir, gatewaytest.FromAPI(t, `{
			"anthropic":{"id":"anthropic","models":{}},
			"openai":{"id":"openai","models":{}},
			"api-key-only":{"id":"api-key-only","npm":"@ai-sdk/openai-compatible","api":"https://api.example/v1","models":{}}
		}`))
		return nil, nil
	})
	if !slices.Contains(a.Accounts().Providers(), agent.ProviderID("api-key-only")) {
		t.Fatal("setup: API-key-only provider is not registered")
	}
	show := bannerShown(t, s.ShowLoginMenu())
	if got, want := bannerNames(show), []string{"anthropic", "openai"}; !slices.Equal(got, want) {
		t.Fatalf("login providers = %v, want %v", got, want)
	}
}

func TestAccountMenu(t *testing.T) {
	s, a := newTestService(t)
	if _, names := accountMenu(t, s); !slices.Equal(names, []string{addAccountName}) {
		t.Fatalf("menu without accounts lists %q, want only %q", names, addAccountName)
	}

	older, newer := addKey(t, a), addKey(t, a)
	show, names := accountMenu(t, s)
	if want := []string{string(older), string(newer), addAccountName}; !slices.Equal(names, want) {
		t.Fatalf("menu lists %q, want %q", names, want)
	}
	if show.SelectedIdx != 1 {
		t.Errorf("menu selects %d, want the active newer account at 1", show.SelectedIdx)
	}

	s.HandleBannerSelect(show.Items[0].Value)
	if st := openAIStatus(t, a); st.Active != older {
		t.Errorf("active account = %q, want %q", st.Active, older)
	}
	if s.state.Banner != NoBanner {
		t.Error("menu stayed open")
	}
	if want := "openai/model"; s.state.ModelLabel != want {
		t.Errorf("model label = %q, want the model alone, %q", s.state.ModelLabel, want)
	}
}

// TestModelLabel checks that the label under the input and /status show the
// model alone, whatever account is active.
func TestModelLabel(t *testing.T) {
	s, a := newTestService(t)
	s.InitSession(t.Context(), "")
	id := addKey(t, a)
	events := s.ShowInfo()
	i := slices.IndexFunc(events, func(ev UIEvent) bool { _, ok := toastOf(ev); return ok })
	if i < 0 {
		t.Fatal("/status shows no notice")
	}
	status, _ := toastOf(events[i])
	if !slices.ContainsFunc(status.Lines, func(l style.StyledText) bool { return l.PlainText() == "Model     openai/model" }) {
		t.Errorf("/status lines = %+v, want the model alone", status.Lines)
	}

	out := s.ApplyAgentEvent(app.EventAccountsChanged{Provider: chatgpt.ProviderID, Active: id})
	if slices.ContainsFunc(out, func(ev UIEvent) bool { _, ok := ev.(EventModelLabelChanged); return ok }) {
		t.Error("the label under the input changed with the accounts")
	}
	if want := "openai/model"; s.state.ModelLabel != want {
		t.Errorf("label under the input = %q, want %q", s.state.ModelLabel, want)
	}
}

func TestApplyLoginPrompt(t *testing.T) {
	s := &Service{state: &UIState{}}
	out := s.ApplyAgentEvent(app.EventLoginPrompt{URL: "https://login.test/device", Code: "ABCD"})
	if len(out) == 0 {
		t.Fatal("no UI events")
	}
	update, ok := out[0].(EventBannerUpdate)
	if !ok {
		t.Fatalf("first event is %T, want EventBannerUpdate", out[0])
	}
	if want := "open https://login.test/device and enter code ABCD"; update.Text != want {
		t.Errorf("banner text = %q, want %q", update.Text, want)
	}
}

func TestApplyManualLoginPrompt(t *testing.T) {
	s, _ := newTestService(t)
	s.OpenBanner(LoginBanner)
	s.ApplyAgentEvent(app.EventLoginPrompt{
		Provider: claude.ProviderID, Account: agent.NewAccountID(),
		URL: "https://claude.ai/oauth/authorize", State: &agent.LoginState{Verifier: "proof", State: "nonce"},
	})
	if s.state.Login == nil || s.state.Banner != LoginBanner || s.state.Prompt != nil {
		t.Fatal("manual login did not keep its state in the input panel")
	}
	if len(s.state.Messages) != 0 {
		t.Fatal("authorization URL was inserted into chat")
	}
	s.HandleEscape()
	if len(s.state.Queue) != 0 {
		t.Fatal("authorization code was queued as a model message")
	}
}

func TestSettleLogin(t *testing.T) {
	t.Run("login banner", func(t *testing.T) {
		state := &UIState{Banner: LoginBanner}
		s := &Service{state: state}

		events := s.SettleLogin("logged in")

		if state.Banner != NoBanner {
			t.Fatal("login banner remained visible")
		}
		if len(events) != 2 {
			t.Fatalf("got %d events, want banner hide and toast", len(events))
		}
		if _, ok := events[0].(EventBannerHide); !ok {
			t.Fatalf("first event is %T, want EventBannerHide", events[0])
		}
	})

	t.Run("unrelated banner", func(t *testing.T) {
		menu := &SearchMenu{}
		state := &UIState{Banner: MenuBanner, Menu: menu}
		s := &Service{state: state}

		events := s.SettleLogin("logged in")

		if state.Banner != MenuBanner || state.Menu != menu {
			t.Fatal("unrelated banner was hidden")
		}
		if len(events) != 1 {
			t.Fatalf("got %d events, want toast only", len(events))
		}
		if _, ok := toastOf(events[0]); !ok {
			t.Fatalf("event is %#v, want a toast", events[0])
		}
	})
}
