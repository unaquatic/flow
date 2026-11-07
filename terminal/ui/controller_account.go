package ui

import (
	"fmt"

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
		return s.ShowToast(fmt.Sprintf("reading the %s accounts failed: %v", pt, err))
	}

	items := make([]MenuItem, 0, len(st.Accounts)+1)
	for _, a := range st.Accounts {
		desc := fmt.Sprintf("%s, %s", a.Kind, a.Source)
		if a.ID == st.Active {
			desc += ", active"
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
		Title: fmt.Sprintf("%s accounts", pt),
		Items: func() []MenuItem { return items },
	})
}

// UseAccount makes account id the active account of pt.
func (s *Service) UseAccount(pt agent.ProviderID, id agent.AccountID) []UIEvent {
	if _, err := s.app.Accounts().Use(s.ctx, pt, id); err != nil {
		return s.ShowToast(fmt.Sprintf("switching to %s account %s failed: %v", pt, id, err))
	}
	return s.ShowToast(fmt.Sprintf("Active %s account: %s.", pt, id))
}
