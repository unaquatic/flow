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
	id, err := s.app.Accounts().StartLogin(pt, "", method)
	if err != nil {
		return s.ShowToast(fmt.Sprintf("login to %s failed: %v", s.ProviderName(pt), err))
	}
	s.OpenBanner(LoginBanner)
	s.state.Prompt = nil
	progress := EventBannerShow{
		Title: fmt.Sprintf("Login with %s", s.ProviderName(pt)),
		Text:  fmt.Sprintf("Logging in to %s account %s [%s]...", s.ProviderName(pt), id, method),
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
	text := "Approve access in your browser, then paste the complete code below.\n" + p.URL
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
		return []UIEvent{s.LoginPanel("Paste the complete code (code#state) or callback URL.")}
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

// ProviderName returns the catalog's name of provider pt, such as "OpenAI",
// or its ID when the catalog gives it none.
func (s *Service) ProviderName(pt agent.ProviderID) string {
	if p, ok := s.app.Models().Catalog().Providers[pt]; ok && p.Name != "" {
		return p.Name
	}
	return string(pt)
}

// ProviderItems returns OAuth-capable providers in ID order, each named as
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
// from the input: logging in through the browser or with a device code, or
// entering an API key.
func (s *Service) ShowLoginMethodMenu(pt agent.ProviderID) []UIEvent {
	login := func(method agent.LoginMethod) func() []UIEvent {
		return s.Closing(func() []UIEvent { return s.StartLogin(pt, method) })
	}
	items := []MenuItem{
		{Name: "Browser", Choose: login(agent.LoginBrowser)},
		{Name: "Device code", Choose: login(agent.LoginDevice)},
		{Name: apiKeyName, Choose: s.Closing(func() []UIEvent { return s.AskKey(pt) })},
	}
	return s.ShowSearchMenu(&SearchMenu{
		Title: fmt.Sprintf("New %s account", s.ProviderName(pt)),
		Items: func() []MenuItem { return items },
	})
}

// AskKey asks for an API key of pt and saves it as a new account.
func (s *Service) AskKey(pt agent.ProviderID) []UIEvent {
	question := fmt.Sprintf("Paste the %s API key into the input and press enter.", s.ProviderName(pt))
	return s.AskText(question, PromptHintStyled("save API key"), func(key string) []UIEvent {
		return s.SubmitKey(pt, key)
	})
}

// SubmitKey saves key as the API key of a new account of pt, which makes the
// account active. The key is not sent to the model.
func (s *Service) SubmitKey(pt agent.ProviderID, key string) []UIEvent {
	id, err := s.app.Accounts().SetKey(s.ctx, pt, "", agent.APIKey(key))
	if err != nil {
		return s.ShowToast(fmt.Sprintf("saving the %s API key failed: %v", s.ProviderName(pt), err))
	}
	return s.ShowToast(fmt.Sprintf("Saved the API key as %s account %s; it is now active.", s.ProviderName(pt), id))
}

// SettleLogin hides an open login banner and reports the result in the
// transcript.
func (s *Service) SettleLogin(text string) []UIEvent {
	if s.state.Banner != LoginBanner {
		return s.ShowToast(text)
	}
	return append(s.CancelBanner(), s.ShowToast(text)...)
}
