package ui

import (
	"fmt"
	"slices"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/fluid/widget"
)

// manualCode is the value of the login banner's item to enter the code of
// a browser login by hand.
const manualCode = "manual"

// apiKeyName names the login method menu row that asks for an API key.
const apiKeyName = "API key"

// StartLogin starts a login to a new account of pt and shows its progress in
// the login banner.
func (s *Service) StartLogin(pt agent.ProviderID, method agent.LoginMethod) []UIEvent {
	id, err := s.app.Accounts().StartLogin(pt, "", method)
	if err != nil {
		return s.ShowToast(fmt.Sprintf("login to %s failed: %v", pt, err))
	}
	s.OpenBanner(LoginBanner)
	progress := EventBannerShow{
		Title: fmt.Sprintf("Login with %s", pt),
		Text:  fmt.Sprintf("Logging in to %s account %s [%s]...", pt, id, method),
	}
	if method == agent.LoginBrowser {
		progress.Items = []widget.SelectItem{{Name: "Enter code manually", Value: manualCode}}
	}
	return []UIEvent{progress}
}

// ShowLoginMenu presents the providers available for login, searched
// from the input.
func (s *Service) ShowLoginMenu() []UIEvent {
	return s.ShowSearchMenu(&SearchMenu{Title: "Login", Items: s.ProviderItems})
}

// ProviderItems returns a row for each provider, in alphabetical order so
// that providers ranking equally in a search keep a stable order. Choosing
// one shows its accounts.
func (s *Service) ProviderItems() []MenuItem {
	providers := s.app.Accounts().Providers()
	slices.Sort(providers)
	items := make([]MenuItem, 0, len(providers))
	for _, pt := range providers {
		items = append(items, MenuItem{
			Name:   string(pt),
			Choose: func() []UIEvent { return s.ShowAccountMenu(pt) },
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
		Title: fmt.Sprintf("New %s account", pt),
		Items: func() []MenuItem { return items },
	})
}

// AskKey asks for an API key of pt and saves it as a new account.
func (s *Service) AskKey(pt agent.ProviderID) []UIEvent {
	question := fmt.Sprintf("Paste the %s API key into the input and press enter.", pt)
	return s.AskText(question, PromptHintStyled("save API key"), func(key string) []UIEvent {
		return s.SubmitKey(pt, key)
	})
}

// SubmitKey saves key as the API key of a new account of pt, which makes the
// account active. The key is not sent to the model.
func (s *Service) SubmitKey(pt agent.ProviderID, key string) []UIEvent {
	id, err := s.app.Accounts().SetKey(s.ctx, pt, "", agent.APIKey(key))
	if err != nil {
		return s.ShowToast(fmt.Sprintf("saving the %s API key failed: %v", pt, err))
	}
	return s.ShowToast(fmt.Sprintf("Saved the API key as %s account %s; it is now active.", pt, id))
}

// SettleLogin hides an open login banner and reports the result in the
// transcript.
func (s *Service) SettleLogin(text string) []UIEvent {
	if s.state.Banner != LoginBanner {
		return s.ShowToast(text)
	}
	return append(s.CancelBanner(), s.ShowToast(text)...)
}
