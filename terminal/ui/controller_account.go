package ui

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/unaquatic/flow/agent"
)

// addAccountName names the account menu row that adds an account instead of
// switching to one.
const addAccountName = "Add account"

// ShowAccountMenu lists the accounts of pt, searched from the input, selecting
// the active one, followed by a row to add an account. Choosing an account
// makes it active.
func (s *Service) ShowAccountMenu(pt agent.ProviderID) []UIEvent {
	st, err := s.app.Accounts().Status(s.ctx, pt)
	if err != nil {
		return s.ShowToast(fmt.Sprintf("Reading the %s accounts failed: %v", s.ProviderName(pt), err))
	}

	items := make([]MenuItem, 0, len(st.Accounts)+1)
	for _, a := range st.Accounts {
		desc := Tag(string(a.Kind)) + " " + Tag(string(a.Source))
		if a.ID == st.Active {
			desc += " " + Tag("active")
		}
		items = append(items, MenuItem{
			Name:     string(a.ID),
			Desc:     desc,
			Selected: a.ID == st.Active,
			Choose:   s.Closing(func() []UIEvent { return s.UseAccount(pt, a.ID) }),
		})
	}
	items = append(items, MenuItem{
		Name:   addAccountName,
		Choose: func() []UIEvent { return s.ShowLoginMethodMenu(pt) },
	})
	return s.ShowSearchMenu(&SearchMenu{
		Title: fmt.Sprintf("%s accounts", s.ProviderName(pt)),
		Items: func() []MenuItem { return items },
	})
}

// CredentialName returns the name of credential kind k that the session
// info shows, such as "OAuth" or "API Key", or k itself for a kind it does
// not know.
func CredentialName(k agent.CredentialKind) string {
	switch k {
	case agent.CredentialOAuth:
		return "OAuth"
	case agent.CredentialAPIKey:
		return "API Key"
	}
	return string(k)
}

// AccountName returns the catalog's name of pt, or Unknown of its ID when
// the catalog does not list it, and then the kind of its active account as
// CredentialName names it, such as "Provider A OAuth", or the provider's
// name alone when it has no active account or its accounts cannot be read.
func (s *Service) AccountName(pt agent.ProviderID) string {
	name := Unknown(string(pt))
	if p, ok := s.app.Models().Catalog().Providers[pt]; ok {
		name = cmp.Or(p.Name, string(pt))
	}
	st, err := s.app.Accounts().Status(s.ctx, pt)
	if err != nil {
		return name
	}
	i := slices.IndexFunc(st.Accounts, func(a agent.AccountStatus) bool { return a.ID == st.Active })
	if i < 0 {
		return name
	}
	return name + " " + CredentialName(st.Accounts[i].Kind)
}

// UseAccount makes account id the active account of pt.
func (s *Service) UseAccount(pt agent.ProviderID, id agent.AccountID) []UIEvent {
	if _, err := s.app.Accounts().Use(s.ctx, pt, id); err != nil {
		return s.ShowToast(fmt.Sprintf("Switching the %s account failed: %v", s.ProviderName(pt), err))
	}
	return s.ShowToast(fmt.Sprintf("Active %s account: %s.", s.ProviderName(pt), id))
}
