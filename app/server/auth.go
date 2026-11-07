package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/unaquatic/flow/agent"
)

// handleAuthList reports the accounts of every provider.
func (s *Server) handleAuthList(w http.ResponseWriter, r *http.Request) {
	types := s.app.Accounts().Providers()
	slices.Sort(types)
	out := make([]AuthStatus, 0, len(types))
	for _, t := range types {
		st, err := s.app.Accounts().Status(r.Context(), t)
		if err != nil {
			respondAuthError(w, err)
			return
		}
		out = append(out, wireAuthStatus(st))
	}
	respondJSON(w, http.StatusOK, out)
}

// handleAuthAdd adds an account by starting a login, answering 202 before it
// ends, or by saving an API key, answering 201. Either way the response holds
// the new account's ID. A login's prompt and outcome arrive on GET /event.
func (s *Server) handleAuthAdd(w http.ResponseWriter, r *http.Request) {
	var req NewAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	switch {
	case (req.Method == "") == (req.Key == ""):
		respondError(w, http.StatusBadRequest, "set exactly one of method and key")
	case req.Method != "":
		s.startLogin(w, r, "", req.Method)
	default:
		id, err := s.app.Accounts().SetKey(r.Context(), providerID(r), "", agent.APIKey(req.Key))
		if err != nil {
			respondAuthError(w, err)
			return
		}
		respondJSON(w, http.StatusCreated, AccountResponse{ID: id})
	}
}

// handleAuthLogin starts a login to an existing account and answers before
// it ends. Its prompt and outcome arrive on GET /event.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Method == "" {
		respondError(w, http.StatusBadRequest, "method must not be empty")
		return
	}
	s.startLogin(w, r, id, req.Method)
}

// startLogin starts a login to account id, or to a new account when id is
// empty, and answers 202 with the account's ID.
func (s *Server) startLogin(w http.ResponseWriter, r *http.Request, id agent.AccountID, m agent.LoginMethod) {
	id, err := s.app.Accounts().StartLogin(providerID(r), id, m)
	if err != nil {
		respondAuthError(w, err)
		return
	}
	respondJSON(w, http.StatusAccepted, AccountResponse{ID: id})
}

// handleAuthKey replaces the API key of an existing account and reports the
// resulting accounts.
func (s *Server) handleAuthKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	var req KeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	t := providerID(r)
	if _, err := s.app.Accounts().SetKey(r.Context(), t, id, agent.APIKey(req.Key)); err != nil {
		respondAuthError(w, err)
		return
	}
	st, err := s.app.Accounts().Status(r.Context(), t)
	respondAuthResult(w, st, err)
}

// handleAuthLogout removes an account and reports the resulting accounts.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	id, ok := pathAccountID(w, r)
	if !ok {
		return
	}
	st, err := s.app.Accounts().Logout(r.Context(), providerID(r), id)
	respondAuthResult(w, st, err)
}

// handleAuthUse switches the active account and reports the resulting
// accounts.
func (s *Server) handleAuthUse(w http.ResponseWriter, r *http.Request) {
	var req UseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.ID == "" {
		respondError(w, http.StatusBadRequest, "id must not be empty")
		return
	}
	st, err := s.app.Accounts().Use(r.Context(), providerID(r), req.ID)
	respondAuthResult(w, st, err)
}

// pathAccountID returns the account ID in the request's path, or writes a 400
// response and reports false when it is not a UUID.
func pathAccountID(w http.ResponseWriter, r *http.Request) (agent.AccountID, bool) {
	id, err := agent.ParseAccountID(r.PathValue("id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return id, true
}

// wireAuthStatus converts a provider's accounts to their wire form.
func wireAuthStatus(st agent.AuthStatus) AuthStatus {
	out := AuthStatus{Provider: st.Provider, Active: st.Active, Accounts: make([]AccountStatus, 0, len(st.Accounts))}
	for _, a := range st.Accounts {
		out.Accounts = append(out.Accounts, AccountStatus(a))
	}
	return out
}

// respondAuthResult writes the accounts after a change, or the failure.
func respondAuthResult(w http.ResponseWriter, st agent.AuthStatus, err error) {
	if err != nil {
		respondAuthError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, wireAuthStatus(st))
}

// respondAuthError maps authentication failures onto HTTP status codes.
func respondAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrProviderNotRegistered),
		errors.Is(err, agent.ErrUnknownAccount):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, agent.ErrEmptyKey),
		errors.Is(err, agent.ErrInvalidAccountID):
		respondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agent.ErrReadOnlyAccount):
		respondError(w, http.StatusConflict, err.Error())
	default:
		respondError(w, http.StatusInternalServerError, err.Error())
	}
}

// providerID returns the provider ID in the request's path.
func providerID(r *http.Request) agent.ProviderID {
	return agent.ProviderID(r.PathValue("provider"))
}

// AuthStatus is the wire form of a provider's accounts.
type AuthStatus struct {
	// Provider identifies the provider.
	Provider agent.ProviderID `json:"provider"`
	// Active identifies the account that requests use. It is absent when
	// the provider has no account.
	Active agent.AccountID `json:"active,omitempty"`
	// Accounts lists the provider's accounts by ID.
	Accounts []AccountStatus `json:"accounts"`
}

// AccountStatus is the wire form of one account.
type AccountStatus struct {
	// ID identifies the account, a UUID.
	ID agent.AccountID `json:"id"`
	// Kind is "api_key" or "oauth".
	Kind agent.CredentialKind `json:"kind"`
	// Source is "config" for the read-only account, with the Nil UUID, that
	// holds an API key set in the configuration or environment, and "store"
	// for an account saved by login.
	Source agent.CredentialSource `json:"source"`
}

// NewAccountRequest is the body of POST /auth/{provider}/accounts. Exactly
// one of Method and Key is set.
type NewAccountRequest struct {
	// Method is "browser" or "device" to add the account by logging in.
	Method agent.LoginMethod `json:"method,omitempty"`
	// Key is an API key to add as the account.
	Key string `json:"key,omitempty"`
}

// AccountResponse identifies the account a request adds or logs in to.
type AccountResponse struct {
	// ID identifies the account.
	ID agent.AccountID `json:"id"`
}

// LoginRequest is the body of POST /auth/{provider}/accounts/{id}/login.
type LoginRequest struct {
	// Method is "browser" or "device".
	Method agent.LoginMethod `json:"method"`
}

// KeyRequest is the body of PUT /auth/{provider}/accounts/{id}/key.
type KeyRequest struct {
	// Key is the API key to save.
	Key string `json:"key"`
}

// UseRequest is the body of PUT /auth/{provider}/active.
type UseRequest struct {
	// ID identifies the account to make active.
	ID agent.AccountID `json:"id"`
}
