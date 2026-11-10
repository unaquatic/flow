package ui

import (
	"fmt"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
)

// apiKeyName names the login method menu row that asks for an API key.
const apiKeyName = "API key"

// StartLogin starts a login to a new account of pt and shows its progress in
// the login banner.
func (s *Service) StartLogin(pt agent.ProviderID, method agent.LoginMethod) []UIEvent {
	if _, err := s.app.Accounts().StartLogin(pt, "", method); err != nil {
		return s.ShowToast(fmt.Sprintf("Login to %s failed: %v", s.ProviderName(pt), err))
	}
	s.OpenBanner(LoginBanner)
	s.state.Prompt = nil
	progress := EventBannerShow{
		Title: fmt.Sprintf("Login with %s", s.ProviderName(pt)),
		Text:  fmt.Sprintf("Logging in [%s]...", method),
	}
	return []UIEvent{progress}
}

// AskLoginCode collects the authorization result without sending it to the model.
func (s *Service) AskLoginCode(prompt app.EventLoginPrompt) []UIEvent {
	s.OpenBanner(LoginBanner)
	s.state.Prompt = nil
	s.state.Login = &prompt
	return append(s.ClearInput(), EventInputHintChanged{}, s.LoginPanel(""))
}

// LoginPanel displays the pending authorization above the input, never in chat.
func (s *Service) LoginPanel(reason string) EventBannerShow {
	p := s.state.Login
	text := "Approve access in your browser, then paste the code below.\n" + p.URL
	if reason != "" {
		text += "\n" + reason
	}
	return EventBannerShow{
		Title: fmt.Sprintf("Login with %s", s.ProviderName(p.Provider)), Text: text, Input: true,
		Keys: []KeyHint{{Key: "enter", Action: "submit"}, {Key: "ctrl+l", Action: "copy"}, HintCancel},
	}
}

// SubmitLoginCode exchanges the input using the proof kept in temporary UI state.
func (s *Service) SubmitLoginCode(code string) []UIEvent {
	p := s.state.Login
	if s.state.LoginCompleting {
		return nil
	}
	if code == "" {
		return []UIEvent{s.LoginPanel("Paste the code or callback URL.")}
	}
	s.state.LoginCompleting = true
	s.app.Accounts().StartCompleteLogin(p.Provider, p.Account, *p.State, code)
	return append(s.ClearInput(), s.LoginPanel("Completing login..."))
}

// ShowLoginMenu presents the providers available for login, searched
// from the input.
func (s *Service) ShowLoginMenu() []UIEvent {
	return s.ShowSearchMenu(&SearchMenu{Title: "Login", Items: s.ProviderItems})
}

// ProviderName returns the catalog's name of provider pt, such as
// "Provider A", or its ID when the catalog gives it none, as
// app.Models.ProviderName does.
func (s *Service) ProviderName(pt agent.ProviderID) string {
	return s.app.Models().ProviderName(pt)
}

// ProviderItems returns the providers of the login menu, as
// app.Accounts.LoginProviders lists them, in ID order, each named as
// ProviderName names it, which the search finds by its ID too. Choosing one
// shows its accounts.
func (s *Service) ProviderItems() []MenuItem {
	providers := s.app.Accounts().LoginProviders()
	items := make([]MenuItem, 0, len(providers))
	for _, pt := range providers {
		items = append(items, MenuItem{
			Name:    s.ProviderName(pt),
			Aliases: []string{string(pt)},
			Choose:  func() []UIEvent { return s.ShowAccountMenu(pt) },
		})
	}
	return items
}

// ShowLoginMethodMenu presents the ways to add an account of pt, searched
// from the input: logging in through the browser or with a device code, when
// pt has a login flow, or entering an API key.
func (s *Service) ShowLoginMethodMenu(pt agent.ProviderID) []UIEvent {
	login := func(method agent.LoginMethod) func() []UIEvent {
		return s.Closing(func() []UIEvent { return s.StartLogin(pt, method) })
	}
	var items []MenuItem
	if s.app.Accounts().SupportsLogin(pt) {
		items = append(items,
			MenuItem{Name: "Browser", Choose: login(agent.LoginBrowser)},
			MenuItem{Name: "Device code", Choose: login(agent.LoginDevice)},
		)
	}
	items = append(items, MenuItem{Name: apiKeyName, Choose: s.Closing(func() []UIEvent { return s.AskKey(pt) })})
	return s.ShowSearchMenu(&SearchMenu{
		Title: fmt.Sprintf("New %s account", s.ProviderName(pt)),
		Items: func() []MenuItem { return items },
	})
}

// AskKey asks for an API key of pt and saves it as a new account.
func (s *Service) AskKey(pt agent.ProviderID) []UIEvent {
	return s.AskText("Paste the API key", PromptHintStyled("save API key"), func(key string) []UIEvent {
		return s.SubmitKey(pt, key)
	})
}

// SubmitKey saves key as the API key of a new account of pt, which makes the
// account active. The key is not sent to the model.
func (s *Service) SubmitKey(pt agent.ProviderID, key string) []UIEvent {
	if _, err := s.app.Accounts().SetKey(s.ctx, pt, "", agent.APIKey(key)); err != nil {
		return s.ShowToast(fmt.Sprintf("Saving the API key failed: %v", err))
	}
	return s.ShowToast("API key saved")
}

// SettleLogin hides an open login banner and reports the result in the
// transcript.
func (s *Service) SettleLogin(text string) []UIEvent {
	if s.state.Banner != LoginBanner {
		return s.ShowToast(text)
	}
	return append(s.CancelBanner(), s.ShowToast(text)...)
}

// FailLogin hides an open login banner and shows the error e reports in the
// indicator, as a run's error shows: ErrorIndicator of it, after
// LoginFailedPrefix, such as "login to Provider A failed: token endpoint
// error (401)" over the body the endpoint answered with.
// While a run is in progress, whose time the indicator shows, it reports the
// error in the transcript instead, on one line, as SettleLogin does.
func (s *Service) FailLogin(e app.EventLoginFailed) []UIEvent {
	prefix := s.LoginFailedPrefix(e.Provider)
	if s.Streaming() {
		return s.SettleLogin(prefix + e.Error.Message)
	}
	var out []UIEvent
	if s.state.Banner == LoginBanner {
		out = s.CancelBanner()
	}
	s.state.Working = false
	ind := ErrorIndicator(e.Error)
	ind.Text = prefix + ind.Text
	return append(out, ind)
}

// LoginFailedPrefix returns what heads the error of a failed login to pt,
// after the indicator's Error label, such as "login to Provider A failed: ".
func (s *Service) LoginFailedPrefix(pt agent.ProviderID) string {
	return fmt.Sprintf("login to %s failed: ", s.ProviderName(pt))
}
