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
	"github.com/unaquatic/flow/extension/protocol/login/codeflow"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/style"
)

// newTestService returns a Service over an isolated app of testCatalog,
// whose providers no API key in the environment authorizes, which logs in
// to them as loginTestFlows does, and whose model is provider-a/model.
func newTestService(t *testing.T, loads ...extension.Load) (*Service, *app.App) {
	t.Helper()
	return newTestServiceWith(t, app.Options{Extensions: loads})
}

// newTestServiceWith returns a Service as newTestService does, over an app
// of opts, whose extensions load after the protocol and loginTestFlows.
func newTestServiceWith(t *testing.T, opts app.Options) (*Service, *app.App) {
	t.Helper()
	t.Chdir(t.TempDir())
	home := t.TempDir()
	settings := `{"default_model":"model","default_provider":"provider-a","snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestCatalog(t, home)
	conf := config.NewService(home)
	opts.Extensions = append([]extension.Load{protocol.Load, loadLoginTestFlows}, opts.Extensions...)
	a, err := app.New(t.Context(), conf, opts)
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
	// Flow is provider-b's login under test.
	Flow agent.LoginFlow
}

// Name identifies the test extension.
func (e *LoginTestExtension) Name() string { return "test_login" }

// LoginFlows replaces provider-b's sign-in for the test app.
func (e *LoginTestExtension) LoginFlows() map[agent.ProviderID]agent.LoginFlow {
	return map[agent.ProviderID]agent.LoginFlow{providerB: e.Flow}
}

// TestManualLoginLifecycle runs a login whose code is pasted, as package
// codeflow's OAuth takes it, at endpoints of the test's own.
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
			oauth := codeflow.NewOAuth()
			oauth.AuthURL = server.URL + "/oauth/authorize"
			oauth.TokenURL = server.URL
			oauth.OpenBrowser = func(string) error { return nil }
			ui := newInputTestUI(t, func(context.Context, extension.Host) (extension.Extension, error) {
				return &LoginTestExtension{Flow: oauth}, nil
			})
			s, a := ui.service, ui.service.app
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			events := a.Subscribe(ctx)
			if _, err := a.Accounts().StartLogin(providerB, "", agent.LoginBrowser); err != nil {
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

// providerAStatus returns the provider-a accounts of a.
func providerAStatus(t *testing.T, a *app.App) agent.AuthStatus {
	t.Helper()
	st, err := a.Accounts().Status(t.Context(), providerA)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// addKey saves an API key as a new provider-a account of a and returns its ID.
func addKey(t *testing.T, a *app.App) agent.AccountID {
	t.Helper()
	id, err := a.Accounts().SetKey(t.Context(), providerA, "", "sk")
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

// askKey returns the steps that open /login and choose provider-a, Add account
// and API key, which leaves the API key question open.
func askKey() []step {
	return []step{submit("/login"), choose("Provider A"), choose(addAccountName), choose(apiKeyName)}
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

	st := providerAStatus(t, a)
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
			if st := providerAStatus(t, a); len(st.Accounts) != 0 {
				t.Errorf("accounts = %+v, want none", st.Accounts)
			}
		})
	}
}

// accountMenu opens the provider-a account menu of s and returns its banner and
// the names of its options.
func accountMenu(t *testing.T, s *Service) (EventBannerShow, []string) {
	t.Helper()
	out := s.ShowAccountMenu(providerA)
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
	all := []string{"Provider A", "Provider B"}
	if got := bannerNames(show); !show.Search || show.Title != "Login" || !slices.Equal(got, all) {
		t.Fatalf("menu = %+v, want %q under a search field titled Login", show, all)
	}

	tests := []struct {
		query    string
		want     []string
		wantText string
	}{
		{query: "pb", want: []string{"Provider B"}},
		{query: "PA", want: []string{"Provider A"}},
		{query: "zzz", wantText: NoMatchesText},
	}
	for _, tt := range tests {
		show := bannerShown(t, s.HandleBannerSearch(tt.query))
		if got := bannerNames(show); !slices.Equal(got, tt.want) || show.Text != tt.wantText {
			t.Errorf("search %q lists %q with text %q, want %q with text %q", tt.query, got, show.Text, tt.want, tt.wantText)
		}
	}

	// The search finds a provider by its ID too.
	if got := bannerNames(bannerShown(t, s.HandleBannerSearch("provider-b"))); len(got) == 0 || got[0] != "Provider B" {
		t.Errorf("search provider-b lists %q, want Provider B first", got)
	}

	bannerShown(t, s.HandleBannerSearch(""))
	bannerShown(t, choose("Provider A")(s))
	if show := bannerShown(t, s.HandleBannerSearch("prov")); len(show.Items) != 0 {
		t.Errorf("search in the account menu lists %q, want its own rows, none of which match", bannerNames(show))
	}
}

// The API-key-only providers of keyOnlyCatalog: keyBuiltIn, which
// TestLoginMenuListsBuiltInKeyProviders makes built in, and keyOnly, which
// no test does.
const (
	keyBuiltIn agent.ProviderID = "gw"
	keyOnly    agent.ProviderID = "api-key-only"
)

// loadKeyOnlyCatalog is an extension.Load that adds none, and caches a
// catalog of provider-a and provider-b, which loginTestFlows logs in to, and
// of keyBuiltIn and keyOnly, which take an API key alone, in place of
// testCatalog.
func loadKeyOnlyCatalog(t *testing.T) extension.Load {
	return func(_ context.Context, host extension.Host) (extension.Extension, error) {
		gatewaytest.WriteCache(t, host.DataDir, protocoltest.FromAPI(t, `{
			"provider-b":{"id":"provider-b","name":"Provider B","format":"messages","models":{}},
			"provider-a":{"id":"provider-a","name":"Provider A","format":"responses","models":{}},
			"gw":{"id":"gw","name":"Gateway","format":"chat","api":"https://gw.example/v1","models":{}},
			"api-key-only":{"id":"api-key-only","format":"chat","api":"https://api.example/v1","models":{}}
		}`))
		return nil, nil
	}
}

func TestLoginMenuOmitsAPIKeyProviders(t *testing.T) {
	s, a := newTestService(t, loadKeyOnlyCatalog(t))
	if !slices.Contains(a.Accounts().Providers(), keyOnly) {
		t.Fatal("setup: API-key-only provider is not registered")
	}
	show := bannerShown(t, s.ShowLoginMenu())
	if got, want := bannerNames(show), []string{"Provider A", "Provider B"}; !slices.Equal(got, want) {
		t.Fatalf("login providers = %v, want %v", got, want)
	}
}

// TestLoginMenuListsBuiltInKeyProviders checks that the login menu lists a
// built-in provider that takes an API key alone, whose method menu offers
// the key alone, and that a key entered through it saves an account.
func TestLoginMenuListsBuiltInKeyProviders(t *testing.T) {
	s, a := newTestServiceWith(t, app.Options{
		BuiltIn:    []agent.ProviderID{keyBuiltIn},
		Extensions: []extension.Load{loadKeyOnlyCatalog(t)},
	})
	show := bannerShown(t, s.ShowLoginMenu())
	if got, want := bannerNames(show), []string{"Gateway", "Provider A", "Provider B"}; !slices.Equal(got, want) {
		t.Fatalf("login providers = %v, want %v: the built-in gw beside those with a login, not api-key-only", got, want)
	}
	if got, want := bannerNames(bannerShown(t, s.ShowLoginMethodMenu(keyBuiltIn))), []string{apiKeyName}; !slices.Equal(got, want) {
		t.Errorf("gw login methods = %v, want %v alone", got, want)
	}
	if got, want := bannerNames(bannerShown(t, s.ShowLoginMethodMenu(providerA))), []string{"Browser", "Device code", apiKeyName}; !slices.Equal(got, want) {
		t.Errorf("provider-a login methods = %v, want %v", got, want)
	}

	run(t, s, submit("/login"), choose("Gateway"), choose(addAccountName), choose(apiKeyName), submit("sk-gw\n"))
	st, err := a.Accounts().Status(t.Context(), keyBuiltIn)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Accounts) != 1 || st.Accounts[0].Kind != agent.CredentialAPIKey || st.Active != st.Accounts[0].ID {
		t.Errorf("gw accounts = %+v, want one active API key", st)
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
	for i, want := range []string{"[api_key] [store]", "[api_key] [store] [active]"} {
		if got := show.Items[i].Description; got != want {
			t.Errorf("account %d reads %q, want %q", i, got, want)
		}
	}

	s.HandleBannerSelect(show.Items[0].Value)
	if st := providerAStatus(t, a); st.Active != older {
		t.Errorf("active account = %q, want %q", st.Active, older)
	}
	if s.state.Banner != NoBanner {
		t.Error("menu stayed open")
	}
	if want := "provider-a/model"; s.state.ModelLabel != want {
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
	if !slices.ContainsFunc(status.Lines, func(l style.StyledText) bool { return l.PlainText() == "Model     provider-a/model" }) {
		t.Errorf("/status lines = %+v, want the model alone", status.Lines)
	}

	out := s.ApplyAgentEvent(app.EventAccountsChanged{Provider: providerA, Active: id})
	if slices.ContainsFunc(out, func(ev UIEvent) bool { _, ok := ev.(EventModelLabelChanged); return ok }) {
		t.Error("the label under the input changed with the accounts")
	}
	if want := "provider-a/model"; s.state.ModelLabel != want {
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
		Provider: providerB, Account: agent.NewAccountID(),
		URL: "https://login.provider-b.test/oauth/authorize", State: &agent.LoginState{Verifier: "proof", State: "nonce"},
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
