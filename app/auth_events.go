package app

import (
	"github.com/unaquatic/flow/agent"
)

const (
	// TypeLoginStarted identifies the start of provider authentication.
	TypeLoginStarted EventType = "auth.login.started"
	// TypeLoginPrompt identifies what the user must do to finish a login.
	TypeLoginPrompt EventType = "auth.login.prompt"
	// TypeLoginSucceeded identifies successful authentication.
	TypeLoginSucceeded EventType = "auth.login.succeeded"
	// TypeLoginFailed identifies failed authentication.
	TypeLoginFailed EventType = "auth.login.failed"
	// TypeAccountsChanged identifies a change to the accounts of a provider.
	TypeAccountsChanged EventType = "auth.accounts.changed"
)

// EventLoginStarted reports the start of a login to a provider account.
type EventLoginStarted struct {
	EventBase
	Provider agent.ProviderID  `json:"provider"`
	Account  agent.AccountID   `json:"account"`
	Method   agent.LoginMethod `json:"method"`
}

func (EventLoginStarted) EventType() EventType { return TypeLoginStarted }

// EventLoginPrompt contains what the user must do to finish a login: open
// URL, and enter Code there when it is set.
type EventLoginPrompt struct {
	EventBase
	Provider agent.ProviderID `json:"provider"`
	Account  agent.AccountID  `json:"account"`
	URL      string           `json:"url"`
	Code     string           `json:"code,omitempty"`
}

func (EventLoginPrompt) EventType() EventType { return TypeLoginPrompt }

// Prompt returns the instruction the event carries.
func (e EventLoginPrompt) Prompt() agent.LoginPrompt {
	return agent.LoginPrompt{URL: e.URL, Code: e.Code}
}

// EventLoginSucceeded reports that a login saved its account.
type EventLoginSucceeded struct {
	EventBase
	Provider agent.ProviderID `json:"provider"`
	Account  agent.AccountID  `json:"account"`
}

func (EventLoginSucceeded) EventType() EventType { return TypeLoginSucceeded }

// EventLoginFailed reports a failed login.
type EventLoginFailed struct {
	EventBase
	Provider agent.ProviderID `json:"provider"`
	Account  agent.AccountID  `json:"account"`
	Error    UnknownError     `json:"error"`
}

func (EventLoginFailed) EventType() EventType { return TypeLoginFailed }

// EventAccountsChanged reports that the accounts of a provider changed
// through this app, and which account is now active. Active is empty when the
// provider has no account left.
type EventAccountsChanged struct {
	EventBase
	Provider agent.ProviderID `json:"provider"`
	Active   agent.AccountID  `json:"active"`
}

func (EventAccountsChanged) EventType() EventType { return TypeAccountsChanged }
