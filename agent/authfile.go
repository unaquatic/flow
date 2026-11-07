package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/unaquatic/flow/config"
)

// fileName is the name of the credential file in AuthFile.Dir.
const fileName = "auth.json"

// AuthFile stores credentials in the auth.json file of a directory, a
// config.File whose top-level keys are providers, each holding the provider's
// AuthEntry. It keeps nothing in memory, and processes that share the
// directory never lose an update or see a partial file, as config.File
// describes.
type AuthFile struct {
	// Dir is the directory holding auth.json, which Update creates, private
	// to its user, when it is missing.
	Dir string
}

// AuthEntry is what auth.json holds for one provider.
type AuthEntry struct {
	// Active identifies the active account. It can identify an account that
	// Accounts lacks, such as ConfigAccount, or be empty.
	Active AccountID
	// Accounts holds the saved credentials by account ID.
	Accounts map[AccountID]Credential
}

// IsZero reports whether e has no active account and no accounts, which
// auth.json leaves out.
func (e AuthEntry) IsZero() bool {
	return e.Active == "" && len(e.Accounts) == 0
}

// entryJSON is the JSON form of an AuthEntry. It lists the accounts in ID
// order.
type entryJSON struct {
	Active   AccountID `json:"active,omitempty"`
	Accounts []record  `json:"accounts,omitempty"`
}

// record is the JSON form of an account and its credential.
type record struct {
	ID           AccountID      `json:"id"`
	Type         CredentialKind `json:"type"`
	Key          string         `json:"key,omitempty"`
	AccessToken  string         `json:"access_token,omitempty"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	ExpiresAt    int64          `json:"expires_at,omitzero"`
}

// Load returns the entry of provider t, which is empty when auth.json has
// none or does not exist. It creates nothing. It fails when auth.json cannot
// be read or the entry is invalid.
func (f AuthFile) Load(t ProviderID) (AuthEntry, error) {
	return f.section(t).Load()
}

// Update applies change to the entry of provider t and saves the result,
// keeping the entries of other providers, as config.Section.Update does: it
// creates auth.json and its directory when missing, saves nothing when change
// fails or leaves auth.json as it was, fails rather than replace an auth.json
// or an entry it cannot parse, and leaves out an entry change empties.
// change gets an entry whose Accounts is never nil. Concurrent updates are
// applied one after another, so change must not call Update of the same
// directory, which would wait for it.
func (f AuthFile) Update(ctx context.Context, t ProviderID, change func(*AuthEntry) error) error {
	return f.section(t).Update(ctx, func(e *AuthEntry) error {
		if e.Accounts == nil {
			e.Accounts = make(map[AccountID]Credential)
		}
		return change(e)
	})
}

// section returns the section of auth.json that holds the entry of provider
// t.
func (f AuthFile) section(t ProviderID) config.Section[AuthEntry] {
	return config.Section[AuthEntry]{File: config.File{Dir: f.Dir, Name: fileName}, Key: string(t)}
}

// UnmarshalJSON decodes the JSON form of an entry into e. Every account
// must have an ID that no other account of the entry has.
func (e *AuthEntry) UnmarshalJSON(data []byte) error {
	var ej entryJSON
	if err := json.Unmarshal(data, &ej); err != nil {
		return err
	}
	out := AuthEntry{Active: ej.Active, Accounts: make(map[AccountID]Credential, len(ej.Accounts))}
	for i, r := range ej.Accounts {
		if r.ID == "" {
			return fmt.Errorf("account %d has no id", i)
		}
		if _, ok := out.Accounts[r.ID]; ok {
			return fmt.Errorf("account %s appears twice", r.ID)
		}
		c, err := decode(r)
		if err != nil {
			return fmt.Errorf("account %s: %w", r.ID, err)
		}
		out.Accounts[r.ID] = c
	}
	*e = out
	return nil
}

// MarshalJSON returns the JSON form of e, which lists its accounts in ID
// order.
func (e AuthEntry) MarshalJSON() ([]byte, error) {
	ej := entryJSON{Active: e.Active, Accounts: make([]record, 0, len(e.Accounts))}
	for _, id := range slices.Sorted(maps.Keys(e.Accounts)) {
		r, err := encode(id, e.Accounts[id])
		if err != nil {
			return nil, fmt.Errorf("account %s: %w", id, err)
		}
		ej.Accounts = append(ej.Accounts, r)
	}
	return json.Marshal(ej)
}

// encode returns the JSON form of account id holding c.
func encode(id AccountID, c Credential) (record, error) {
	switch c := c.(type) {
	case APIKey:
		return record{ID: id, Type: CredentialAPIKey, Key: string(c)}, nil
	case OAuthToken:
		r := record{
			ID:           id,
			Type:         CredentialOAuth,
			AccessToken:  c.Access,
			RefreshToken: c.Refresh,
		}
		if !c.Expiry.IsZero() {
			r.ExpiresAt = c.Expiry.Unix()
		}
		return r, nil
	}
	return record{}, fmt.Errorf("unsupported credential %T", c)
}

// decode returns the credential of r.
func decode(r record) (Credential, error) {
	switch r.Type {
	case CredentialAPIKey:
		if r.Key == "" {
			return nil, errors.New("api_key credential without key")
		}
		return APIKey(r.Key), nil
	case CredentialOAuth:
		if r.AccessToken == "" {
			return nil, errors.New("oauth credential without access_token")
		}
		t := OAuthToken{Access: r.AccessToken, Refresh: r.RefreshToken}
		if r.ExpiresAt != 0 {
			t.Expiry = time.Unix(r.ExpiresAt, 0)
		}
		return t, nil
	}
	return nil, fmt.Errorf("unknown credential type %q", r.Type)
}
